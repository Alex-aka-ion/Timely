-- Журнал уведомлений родителям: что и кому было отправлено (или не
-- доставлено). Нужен преподавателю, чтобы видеть реальную картину — кто
-- получил напоминание/уведомление о переносе, а кому оно не дошло.
-- В отличие от sent_reminders (только дедупликация, без текста) хранит
-- сам текст. Хранится ограниченный хвост — см. LogNotification.
CREATE TABLE notification_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    text       TEXT NOT NULL,
    delivered  INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
