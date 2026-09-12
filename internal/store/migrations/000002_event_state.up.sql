-- Последнее известное планировщику состояние instance-события Calendar.
-- Нужно, чтобы заметить перенос времени или отмену занятия и уведомить
-- родителей: Google Calendar API сам такие уведомления не шлёт, только
-- отдаёт текущий снимок — единственный способ заметить изменение — сравнить
-- с тем, что мы видели на предыдущем тике планировщика.
CREATE TABLE event_state (
    instance_event_id  TEXT PRIMARY KEY,
    start_time         DATETIME NOT NULL,
    notified_cancelled INTEGER NOT NULL DEFAULT 0,
    updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
