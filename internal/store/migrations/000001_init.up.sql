-- Абстрактный пользователь системы.
CREATE TABLE users (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    full_name  TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Аккаунты пользователей в мессенджерах.
-- Один пользователь может иметь несколько аккаунтов в разных мессенджерах.
CREATE TABLE messenger_accounts (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    messenger    TEXT NOT NULL,
    external_id  TEXT NOT NULL,
    username     TEXT,
    is_active    INTEGER NOT NULL DEFAULT 1,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (messenger, external_id)
);

CREATE INDEX idx_messenger_accounts_user_id ON messenger_accounts(user_id);

-- Ученики. Создаются только преподавателем.
-- reminder_intervals: NULL = использовать глобальные из settings.
CREATE TABLE students (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    display_name       TEXT NOT NULL,
    notes              TEXT,
    reminder_intervals TEXT,
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Связь ученика с пользователями (родителями).
-- К одному ученику можно привязать несколько контактов (оба родителя).
CREATE TABLE student_contacts (
    student_id INTEGER NOT NULL REFERENCES students(id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label      TEXT,
    added_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (student_id, user_id)
);

CREATE INDEX idx_student_contacts_user_id ON student_contacts(user_id);

-- Привязка мастер-события Calendar к ученику.
-- Один мастер-event = один ученик. Все instances наследуют привязку.
CREATE TABLE event_students (
    master_event_id TEXT PRIMARY KEY,
    student_id      INTEGER NOT NULL REFERENCES students(id) ON DELETE CASCADE,
    added_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_event_students_student_id ON event_students(student_id);

-- Дедупликация отправленных напоминаний.
-- Уникальность по (instance, user, type) — один и тот же тип напоминания
-- не отправляется одному и тому же пользователю дважды для одного instance.
CREATE TABLE sent_reminders (
    instance_event_id TEXT NOT NULL,
    user_id           INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reminder_type     TEXT NOT NULL,
    sent_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (instance_event_id, user_id, reminder_type)
);

CREATE INDEX idx_sent_reminders_sent_at ON sent_reminders(sent_at);

-- Глобальные настройки key/value.
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Значение по умолчанию для интервалов напоминаний.
INSERT INTO settings (key, value) VALUES ('reminder_intervals', '24h,2h');
