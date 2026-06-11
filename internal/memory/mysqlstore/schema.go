package mysqlstore

const schemaSQL = `
CREATE TABLE IF NOT EXISTS chat_sessions (
  id VARCHAR(64) NOT NULL PRIMARY KEY,
  user_id VARCHAR(64) NULL,
  anonymous_id VARCHAR(64) NULL,
  title VARCHAR(128) NULL,
  status VARCHAR(20) NOT NULL DEFAULT 'active',
  metadata JSON NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  last_message_at DATETIME(3) NULL,
  KEY idx_chat_sessions_user_updated (user_id, updated_at),
  KEY idx_chat_sessions_anon_updated (anonymous_id, updated_at),
  KEY idx_chat_sessions_last_message (last_message_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS chat_messages (
  id VARCHAR(64) NOT NULL PRIMARY KEY,
  session_id VARCHAR(64) NOT NULL,
  role VARCHAR(20) NOT NULL,
  content TEXT NOT NULL,
  metadata JSON NULL,
  created_at DATETIME(3) NOT NULL,
  KEY idx_chat_messages_session_created (session_id, created_at),
  CONSTRAINT fk_chat_messages_session
    FOREIGN KEY (session_id) REFERENCES chat_sessions(id)
    ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`
