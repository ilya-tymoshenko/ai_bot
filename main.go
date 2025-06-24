package main

import (
	"log"
	"os"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
)

func main() {
	// 1. Загружаем .env, чтобы получить TELEGRAM_BOT_TOKEN (и пр.)
	err := godotenv.Load("key.env")
	if err != nil {
		log.Println("Предупреждение: .env файл не найден, используем переменные окружения.")
	}

	// 2. Считываем токен
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if botToken == "" {
		log.Fatal("Не найден TELEGRAM_BOT_TOKEN в переменных окружения или .env")
	}

	// 3. Подключаемся к Telegram
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = false
	log.Printf("Авторизовались как бот %s", bot.Self.UserName)

	// 4. Инициализируем базу SQLite
	initDB("data.db")

	// 5. Инициализируем Qdrant
	initQdrant()

	// 6. Настраиваем Updates (long polling)
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	// 7. Главный цикл
	for update := range updates {
		if update.Message != nil {
			// Вызываем нашу функцию обработки (в handlers.go)
			handleUpdate(bot, update)
		}
	}
}
