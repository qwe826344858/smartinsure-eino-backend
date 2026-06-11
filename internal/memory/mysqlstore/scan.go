package mysqlstore

import (
	"database/sql"
	"encoding/json"
)

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSession(row rowScanner) (*Session, error) {
	var session Session
	var userID, anonymousID, title sql.NullString
	var metadata sql.NullString
	var lastMessageAt sql.NullTime
	err := row.Scan(
		&session.ID,
		&userID,
		&anonymousID,
		&title,
		&session.Status,
		&metadata,
		&session.CreatedAt,
		&session.UpdatedAt,
		&lastMessageAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	session.UserID = userID.String
	session.AnonymousID = anonymousID.String
	session.Title = title.String
	if metadata.Valid {
		session.Metadata = json.RawMessage(metadata.String)
	}
	if lastMessageAt.Valid {
		session.LastMessageAt = &lastMessageAt.Time
	}
	return &session, nil
}

func scanSessions(rows *sql.Rows) ([]Session, error) {
	var sessions []Session
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, *session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}

func scanMessage(row rowScanner) (*Message, error) {
	var message Message
	var metadata sql.NullString
	err := row.Scan(
		&message.ID,
		&message.SessionID,
		&message.Role,
		&message.Content,
		&metadata,
		&message.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if metadata.Valid {
		message.Metadata = json.RawMessage(metadata.String)
	}
	return &message, nil
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	var messages []Message
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, *message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return messages, nil
}
