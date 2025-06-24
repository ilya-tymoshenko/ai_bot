package main

import (
	"database/sql"
	"log"

	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB

// PhoneKeyInfo - хранит корпоративный номер
type PhoneKeyInfo struct {
	PhoneNumber string
	IsActive    bool
	UsedBy      int64 // 0, если не используется
}

// initDB - инициализация SQLite
func initDB(dbPath string) {
	var err error
	db, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatal("Ошибка открытия БД:", err)
	}

	// Создаём таблицу, если нет
	createTable := `CREATE TABLE IF NOT EXISTS phone_keys (
        phone_number TEXT PRIMARY KEY,
        is_active BOOLEAN NOT NULL,
        used_by_user_id INTEGER
    );`
	_, err = db.Exec(createTable)
	if err != nil {
		log.Fatal("Ошибка при создании таблицы phone_keys:", err)
	}
}

// addPhone - добавить (или обновить) запись с номером
func addPhone(phoneNumber string, isActive bool) error {
	_, err := db.Exec(`
        INSERT OR REPLACE INTO phone_keys 
        (phone_number, is_active, used_by_user_id) 
        VALUES (?, ?, NULL)
    `, phoneNumber, isActive)
	return err
}

// getPhoneInfo - получить данные о конкретном номере
func getPhoneInfo(phoneNumber string) (*PhoneKeyInfo, error) {
	row := db.QueryRow(`
        SELECT phone_number, is_active, used_by_user_id 
        FROM phone_keys 
        WHERE phone_number = ?
    `, phoneNumber)

	var info PhoneKeyInfo
	var usedBy sql.NullInt64

	err := row.Scan(&info.PhoneNumber, &info.IsActive, &usedBy)
	if err != nil {
		return nil, err
	}
	if usedBy.Valid {
		info.UsedBy = usedBy.Int64
	} else {
		info.UsedBy = 0
	}
	return &info, nil
}

// getPhoneByUserID - найти номер, занятый конкретным userID
func getPhoneByUserID(userID int64) (*PhoneKeyInfo, error) {
	row := db.QueryRow(`
        SELECT phone_number, is_active, used_by_user_id 
        FROM phone_keys 
        WHERE used_by_user_id = ?
    `, userID)

	var info PhoneKeyInfo
	var usedBy sql.NullInt64

	err := row.Scan(&info.PhoneNumber, &info.IsActive, &usedBy)
	if err != nil {
		return nil, err
	}
	if usedBy.Valid {
		info.UsedBy = usedBy.Int64
	} else {
		info.UsedBy = 0
	}
	return &info, nil
}

// assignPhoneToUser - привязать телефон к userID
func assignPhoneToUser(phoneNumber string, userID int64) error {
	_, err := db.Exec(`
        UPDATE phone_keys
        SET used_by_user_id = ?
        WHERE phone_number = ?
    `, userID, phoneNumber)
	return err
}

// unassignPhone - отвязать телефон (used_by_user_id = NULL)
func unassignPhone(phoneNumber string) error {
	_, err := db.Exec(`
        UPDATE phone_keys
        SET used_by_user_id = NULL
        WHERE phone_number = ?
    `, phoneNumber)
	return err
}
