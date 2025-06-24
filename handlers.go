package main

import (
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/xuri/excelize/v2" // для Excel
)

// phoneRegex - проверяем формат "0XXXXXXXXX"
var phoneRegex = regexp.MustCompile(`^0\d{9}$`)

func validatePhoneNumber(phone string) bool {
	return phoneRegex.MatchString(phone)
}

// ensureStorageDir - гарантируем папку storage/
func ensureStorageDir() error {
	return os.MkdirAll("storage", 0755)
}

// Проверка расширения файла (doc, docx, pdf, txt, xls, xlsx)
func isAllowedFileExt(filename string) bool {
	allowed := []string{".doc", ".docx", ".pdf", ".txt", ".xls", ".xlsx"}
	ext := strings.ToLower(filepath.Ext(filename))
	for _, a := range allowed {
		if ext == a {
			return true
		}
	}
	return false
}

// handleUpdate - обрабатываем сообщение
func handleUpdate(bot *tgbotapi.BotAPI, update tgbotapi.Update) {
	if update.Message == nil {
		return
	}

	chatID := update.Message.Chat.ID
	text := update.Message.Text

	log.Printf("[user %d] -> %s", chatID, text)

	// Если документ + подпись "/upload", обрабатываем загрузку
	if update.Message.Document != nil && update.Message.Caption == "/upload" {
		handleDocumentUpload(bot, update)
		return
	}

	// Иначе проверяем авторизацию
	phoneInfo, err := getPhoneByUserID(chatID)
	if err != nil && err != sql.ErrNoRows {
		log.Println("Ошибка getPhoneByUserID:", err)
		msg := tgbotapi.NewMessage(chatID, "Ошибка БД. Повторите позже.")
		bot.Send(msg)
		return
	}

	if phoneInfo == nil {
		handleNotAuthorized(bot, chatID, text)
	} else {
		handleAuthorizedUser(bot, chatID, text, phoneInfo)
	}
}

// handleDocumentUpload - получение документа
func handleDocumentUpload(bot *tgbotapi.BotAPI, update tgbotapi.Update) {
	chatID := update.Message.Chat.ID

	err := ensureStorageDir()
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "Не удалось создать директорию storage: "+err.Error()))
		return
	}

	doc := update.Message.Document
	if doc == nil {
		return
	}

	// Проверяем расширение
	if !isAllowedFileExt(doc.FileName) {
		msg := "Недопустимый формат файла! Разрешены: doc, docx, pdf, txt, xls, xlsx."
		bot.Send(tgbotapi.NewMessage(chatID, msg))
		return
	}

	// Загружаем файл от Telegram
	fileConfig := tgbotapi.FileConfig{FileID: doc.FileID}
	tgFile, err := bot.GetFile(fileConfig)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "Ошибка: не могу получить файл у Telegram"))
		return
	}

	localPath := "storage/" + doc.FileName
	err = downloadFile("https://api.telegram.org/file/bot"+bot.Token+"/"+tgFile.FilePath, localPath)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "Ошибка скачивания: "+err.Error()))
		return
	}

	// Извлекаем текст
	text, err := extractText(localPath)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "Ошибка извлечения текста: "+err.Error()))
		return
	}

	// Дробим на чанки и индексируем в Qdrant
	chunks := chunkText(text, 500)
	err = indexChunks(chunks)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "Ошибка индексации в Qdrant: "+err.Error()))
		return
	}

	bot.Send(tgbotapi.NewMessage(chatID, "Файл загружен и проиндексирован!"))
}

// handleNotAuthorized - запрос телефона
func handleNotAuthorized(bot *tgbotapi.BotAPI, chatID int64, text string) {
	if !validatePhoneNumber(text) {
		msg := tgbotapi.NewMessage(chatID, "Введите телефон в формате 0XXXXXXXXX:")
		bot.Send(msg)
		return
	}

	phoneNumber := text
	info, err := getPhoneInfo(phoneNumber)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Номер не найден или ошибка БД.")
		bot.Send(msg)
		return
	}

	if !info.IsActive {
		bot.Send(tgbotapi.NewMessage(chatID, "Номер заблокирован/неактивен."))
		return
	}
	if info.UsedBy != 0 && info.UsedBy != chatID {
		bot.Send(tgbotapi.NewMessage(chatID, "Номер уже используется другим сотрудником."))
		return
	}

	err = assignPhoneToUser(phoneNumber, chatID)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "Ошибка при привязке номера."))
		return
	}

	bot.Send(tgbotapi.NewMessage(chatID, "Авторизация успешна. Теперь можете задавать вопросы боту."))
}

// handleAuthorizedUser - пользователь авторизован: ищем в Qdrant
func handleAuthorizedUser(bot *tgbotapi.BotAPI, chatID int64, text string, phoneInfo *PhoneKeyInfo) {
	if !phoneInfo.IsActive {
		_ = unassignPhone(phoneInfo.PhoneNumber)
		bot.Send(tgbotapi.NewMessage(chatID, "Ваш номер заблокирован."))
		return
	}

	questionVec := computeEmbedding(text)
	top, err := searchInQdrant(questionVec, 3)
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "Ошибка Qdrant: "+err.Error()))
		return
	}

	if len(top) == 0 {
		bot.Send(tgbotapi.NewMessage(chatID, "Ничего не найдено. Возможно, нет данных."))
		return
	}

	reply := "Релевантные фрагменты:\n\n"
	for i, sp := range top {
		val := sp.Payload["chunk_text"] // *qdrant.Value
		var chunkText string
		if val != nil {
			chunkText = val.GetStringValue()
		}
		reply += fmt.Sprintf("Фрагмент %d (score=%.4f):\n%s\n\n", i+1, sp.Score, chunkText)
	}

	bot.Send(tgbotapi.NewMessage(chatID, reply))
}

// =====================
// ВСПОМОГАТЕЛЬНЫЕ ФУНКЦИИ
// =====================

// downloadFile - скачивает файл по URL
func downloadFile(url, localPath string) error {
	out, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer out.Close()

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// extractText - смотрит на расширение файла
func extractText(filePath string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".pdf":
		return extractTextFromPDF(filePath)
	case ".doc", ".docx":
		return extractTextFromDocx(filePath)
	case ".xls", ".xlsx":
		return extractTextFromExcel(filePath)
	case ".txt":
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "", err
		}
		return string(data), nil
	default:
		return "", fmt.Errorf("неизвестный формат: %s", ext)
	}
}

// extractTextFromPDF - через pdftotext
func extractTextFromPDF(filePath string) (string, error) {
	txtPath := filePath + ".txt"
	cmd := exec.Command("pdftotext", "-layout", filePath, txtPath)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ошибка pdftotext: %w", err)
	}
	data, err := os.ReadFile(txtPath)
	if err != nil {
		return "", err
	}
	os.Remove(txtPath)
	return string(data), nil
}

// extractTextFromDocx - через LibreOffice (headless), с "до/после" подходом
func extractTextFromDocx(filePath string) (string, error) {
	outDir := filepath.Dir(filePath)

	beforeMap, err := listTxtFiles(outDir)
	if err != nil {
		return "", fmt.Errorf("ошибка при чтении директории %s (до конвертации): %w", outDir, err)
	}

	cmd := exec.Command("soffice",
		"--headless",
		"--convert-to", "txt:Text",
		filePath,
		"--outdir", outDir,
	)
	if out, errRun := cmd.CombinedOutput(); errRun != nil {
		return "", fmt.Errorf("ошибка LibreOffice: %w\nдетали: %s", errRun, string(out))
	}

	afterMap, err := listTxtFiles(outDir)
	if err != nil {
		return "", fmt.Errorf("ошибка при чтении директории %s (после конвертации): %w", outDir, err)
	}

	newTxt := ""
	for name := range afterMap {
		if !beforeMap[name] {
			newTxt = name
			break
		}
	}
	if newTxt == "" {
		return "", fmt.Errorf("LibreOffice создал .txt, но мы его не нашли (нет новых .txt)")
	}

	outPath := filepath.Join(outDir, newTxt)
	data, err := os.ReadFile(outPath)
	if err != nil {
		return "", fmt.Errorf("ошибка чтения %s: %w", outPath, err)
	}
	// os.Remove(outPath) // если нужно удалить

	return string(data), nil
}

// extractTextFromExcel - через excelize
func extractTextFromExcel(filePath string) (string, error) {
	f, err := excelize.OpenFile(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var sb strings.Builder
	sheets := f.GetSheetList()
	for _, sheet := range sheets {
		rows, err := f.GetRows(sheet)
		if err != nil {
			return "", err
		}
		for _, row := range rows {
			sb.WriteString(strings.Join(row, " "))
			sb.WriteString("\n")
		}
	}
	return sb.String(), nil
}

// listTxtFiles - служебная для .docx логики (до/после)
func listTxtFiles(dir string) (map[string]bool, error) {
	result := make(map[string]bool)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".txt") {
			result[e.Name()] = true
		}
	}
	return result, nil
}

// chunkText - разбивает текст на чанки ~по 500 слов
func chunkText(text string, chunkSize int) []string {
	words := strings.Fields(text)
	var chunks []string
	for i := 0; i < len(words); i += chunkSize {
		end := i + chunkSize
		if end > len(words) {
			end = len(words)
		}
		chunk := strings.Join(words[i:end], " ")
		chunks = append(chunks, chunk)
	}
	return chunks
}
