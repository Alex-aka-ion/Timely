// Package scheduler — фоновая горутина, которая опрашивает Google Calendar
// и отправляет напоминания родителям через notify.Dispatcher.
//
// Логика:
//  1. Тик каждые SchedulerTick (по умолчанию 5 мин).
//  2. Получаем instance события на окно [now, now + max(maxInterval + slack,
//     changeDetectionHorizon)] — см. комментарий у changeDetectionHorizon,
//     почему это НЕ то же самое окно, что для напоминаний.
//  3. Для каждого instance:
//     - Пропускаем cancelled.
//     - Находим master_event_id (для повторяющихся: instance.RecurringEventId).
//     - GetStudentForEvent(master) — пропускаем если нет привязки.
//     - Берём интервалы: индивидуальные ученика или глобальные.
//     - Для каждого interval: попадает ли now в окно ±5 мин от (event.Start - interval)?
//     - Проверяем sent_reminders — если уже отправили этот тип, пропускаем.
//     - Dispatcher.SendToUser → MarkReminderSent для каждого активного контакта.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/booking-bot/booking-bot/internal/calendar"
	"github.com/booking-bot/booking-bot/internal/config"
	"github.com/booking-bot/booking-bot/internal/logger"
	"github.com/booking-bot/booking-bot/internal/notify"
	"github.com/booking-bot/booking-bot/internal/store"
)

// matchSlack — половина "окна" совпадения (now ± slack).
// При тике 5 мин — окно ±5 мин гарантирует, что один раз попадём в каждый интервал.
const matchSlack = 5 * time.Minute

// changeDetectionHorizon — насколько далеко вперёд планировщик обязан видеть
// instance, чтобы отслеживать перенос/отмену занятия (detectChange), даже
// если ближайшее напоминание для ученика настроено на совсем короткий
// интервал (например, 5m). Раньше окно запроса к календарю было равно
// maxInterval+slack, поэтому событие, перенесённое на время дальше этого
// окна, просто переставало попадать в UpcomingInstances — event_state для
// него не обновлялся, и родитель никогда не получал уведомление о переносе.
// 14 дней — тот же горизонт, что и у команды /events (upcomingHorizon в
// handler_teacher.go), этого достаточно для любых реалистичных переносов.
const changeDetectionHorizon = 14 * 24 * time.Hour

// Scheduler оркестрирует напоминания.
type Scheduler struct {
	cfg        *config.Config
	store      store.Store
	calClient  calendar.Client
	dispatcher *notify.Dispatcher

	calendarID string
	tick       time.Duration

	nowFunc func() time.Time // для тестов
}

// Deps — зависимости планировщика.
type Deps struct {
	Cfg        *config.Config
	Store      store.Store
	CalClient  calendar.Client
	Dispatcher *notify.Dispatcher
}

// New создаёт планировщик с настройками из конфига.
func New(d Deps) *Scheduler {
	return &Scheduler{
		cfg:        d.Cfg,
		store:      d.Store,
		calClient:  d.CalClient,
		dispatcher: d.Dispatcher,
		calendarID: d.Cfg.GoogleCalendarID,
		tick:       d.Cfg.SchedulerTick,
		nowFunc:    time.Now,
	}
}

// Run работает до отмены контекста. Один тик при старте — чтобы не ждать первый интервал.
func (s *Scheduler) Run(ctx context.Context) error {
	log := logger.FromContext(ctx)
	log.Info("scheduler started", "tick", s.tick.String())

	// Первый запуск.
	s.runOnce(ctx)

	t := time.NewTicker(s.tick)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("scheduler stopped")
			return nil
		case <-t.C:
			s.runOnce(ctx)
		}
	}
}

// runOnce — один проход. Не возвращает ошибку — все ошибки логируются.
func (s *Scheduler) runOnce(ctx context.Context) {
	log := logger.FromContext(ctx)
	now := s.nowFunc()
	maxInt := s.cfg.MaxInterval()
	from := now
	// Окно для detectChange должно быть широким независимо от того, какие
	// интервалы напоминаний настроены — иначе перенос занятия дальше, чем
	// maxInterval+slack, останется незамеченным (см. changeDetectionHorizon).
	reminderWindow := maxInt + 10*time.Minute
	to := now.Add(reminderWindow)
	if changeDetectionHorizon > reminderWindow {
		to = now.Add(changeDetectionHorizon)
	}

	log.Debug("scheduler tick", "from", from, "to", to)

	instances, err := s.calClient.UpcomingInstances(ctx, s.calendarID, from, to)
	if err != nil {
		log.Error("получить instances", "error", err)
		return
	}

	for _, in := range instances {
		s.processInstance(ctx, now, in)
	}
}

// processInstance обрабатывает один instance события.
func (s *Scheduler) processInstance(ctx context.Context, now time.Time, in calendar.Instance) {
	log := logger.FromContext(ctx)

	student, err := s.store.GetStudentForEvent(ctx, in.MasterID)
	if errors.Is(err, store.ErrNotFound) {
		log.Debug("событие без ученика", "instance_id", in.ID, "master_id", in.MasterID)
		return
	}
	if err != nil {
		log.Error("GetStudentForEvent", "error", err)
		return
	}

	// Замечаем перенос времени/отмену ДО фильтрации cancelled ниже — иначе
	// для уже отменённого instance просто вышли бы, так и не уведомив родителей.
	s.detectChange(ctx, in, student)

	if in.Status == calendar.StatusCancelled {
		log.Debug("пропуск cancelled", "instance_id", in.ID)
		return
	}

	intervals, err := s.intervalsFor(ctx, student)
	if err != nil {
		log.Error("intervals", "student", student.ID, "error", err)
		return
	}

	contacts, err := s.store.GetStudentContacts(ctx, student.ID)
	if err != nil {
		log.Error("GetStudentContacts", "error", err)
		return
	}
	if len(contacts) == 0 {
		log.Debug("нет контактов", "student", student.ID, "instance_id", in.ID)
		return
	}

	for _, interval := range intervals {
		fireAt := in.Start.Add(-interval)
		// окно ±matchSlack
		if now.Before(fireAt.Add(-matchSlack)) || now.After(fireAt.Add(matchSlack)) {
			continue
		}
		reminderType := interval.String()
		text := buildText(student.DisplayName, in.Summary, interval, in.Start)

		for _, c := range contacts {
			already, err := s.store.ReminderSent(ctx, in.ID, c.UserID, reminderType)
			if err != nil {
				log.Error("ReminderSent", "user_id", c.UserID, "error", err)
				continue
			}
			if already {
				log.Debug("уже отправляли", "user_id", c.UserID, "type", reminderType)
				continue
			}
			if err := s.dispatcher.SendToUser(ctx, c.UserID, text); err != nil {
				log.Error("SendToUser", "user_id", c.UserID, "error", err)
				// Не прекращаем — пробуем других контактов и не записываем "отправлено".
				continue
			}
			if err := s.store.MarkReminderSent(ctx, in.ID, c.UserID, reminderType); err != nil {
				log.Error("MarkReminderSent", "user_id", c.UserID, "error", err)
			}
			log.Info("reminder sent",
				"instance_id", in.ID,
				"student", student.ID,
				"user_id", c.UserID,
				"interval", reminderType)
		}
	}
}

// intervalsFor возвращает индивидуальные интервалы ученика или глобальные.
func (s *Scheduler) intervalsFor(ctx context.Context, st store.Student) ([]time.Duration, error) {
	if strings.TrimSpace(st.ReminderIntervals) != "" {
		return config.ParseIntervals(st.ReminderIntervals)
	}
	// Глобальные.
	val, err := s.store.GetSetting(ctx, "reminder_intervals")
	if err != nil {
		// Если settings ещё не было — берём из конфига как страховку.
		return s.cfg.ReminderIntervals, nil
	}
	return config.ParseIntervals(val)
}

// buildText формирует текст напоминания.
func buildText(student, summary string, interval time.Duration, start time.Time) string {
	return fmt.Sprintf(
		"Напоминание: занятие у %s\n%s\nЧерез %s (%s)",
		student, summary, humanInterval(interval), formatWhen(start),
	)
}

// formatWhen — единообразное форматирование времени во всех текстах,
// которые планировщик шлёт родителям (напоминания и уведомления об
// изменениях). "MST" — не литерал, а формат-верб time.Format: Go подставит
// туда аббревиатуру (или числовое смещение вроде "+04", если для часового
// пояса в tzdata нет буквенного обозначения) того часового пояса, что
// сейчас в TZ/системе — без привязки к конкретному региону в коде.
func formatWhen(t time.Time) string {
	return t.Local().Format("Mon 02.01 в 15:04 MST")
}

// detectChange сравнивает текущее состояние instance с последним известным
// планировщику (см. store.EventState) и уведомляет привязанных родителей о
// переносе времени или отмене занятия. Google Calendar API не присылает
// такие уведомления сам, отдаёт только текущий снимок состояния — поэтому
// единственный способ заметить изменение — сравнить с тем, что видели на
// предыдущем тике.
//
// Первое появление instance в системе — не изменение, а точка отсчёта:
// ничего не отправляем, просто запоминаем текущее время начала.
func (s *Scheduler) detectChange(ctx context.Context, in calendar.Instance, student store.Student) {
	log := logger.FromContext(ctx)

	prev, err := s.store.GetEventState(ctx, in.ID)
	if errors.Is(err, store.ErrNotFound) {
		if in.Status == calendar.StatusCancelled {
			return // никогда не видели, и оно уже отменено — уведомлять некого
		}
		if err := s.store.SaveEventState(ctx, in.ID, in.Start); err != nil {
			log.Error("SaveEventState", "instance_id", in.ID, "error", err)
		}
		return
	}
	if err != nil {
		log.Error("GetEventState", "instance_id", in.ID, "error", err)
		return
	}
	if prev.NotifiedCancelled {
		return // уже уведомили об отмене этого instance — не повторяем
	}

	switch {
	case in.Status == calendar.StatusCancelled:
		s.notifyContacts(ctx, student, fmt.Sprintf(
			"Занятие у %s отменено.\n%s\nБыло запланировано на %s",
			student.DisplayName, in.Summary, formatWhen(prev.Start)))
		if err := s.store.MarkEventCancelled(ctx, in.ID); err != nil {
			log.Error("MarkEventCancelled", "instance_id", in.ID, "error", err)
		}

	case !in.Start.Equal(prev.Start):
		s.notifyContacts(ctx, student, fmt.Sprintf(
			"Время занятия у %s изменено.\nСтало: %s",
			student.DisplayName, formatWhen(in.Start)))
		if err := s.store.SaveEventState(ctx, in.ID, in.Start); err != nil {
			log.Error("SaveEventState (перенос)", "instance_id", in.ID, "error", err)
		}
		// Напоминания, отправленные под старое время, неактуальны для
		// нового — сбрасываем, чтобы они пересчитались от нового start.
		if err := s.store.ClearRemindersForInstance(ctx, in.ID); err != nil {
			log.Error("ClearRemindersForInstance", "instance_id", in.ID, "error", err)
		}
	}
}

// notifyContacts рассылает текст всем контактам ученика через Dispatcher.
// В отличие от напоминаний, здесь нет дедупликации по типу в БД — вызывающий
// код (detectChange) уже гарантирует, что каждое изменение обрабатывается
// ровно один раз (через notified_cancelled и сравнение start).
func (s *Scheduler) notifyContacts(ctx context.Context, student store.Student, text string) {
	log := logger.FromContext(ctx)
	contacts, err := s.store.GetStudentContacts(ctx, student.ID)
	if err != nil {
		log.Error("GetStudentContacts", "student", student.ID, "error", err)
		return
	}
	for _, c := range contacts {
		if err := s.dispatcher.SendToUser(ctx, c.UserID, text); err != nil {
			log.Error("SendToUser (уведомление об изменении)", "user_id", c.UserID, "error", err)
			continue
		}
		log.Info("уведомление об изменении отправлено", "student", student.ID, "user_id", c.UserID)
	}
}

// humanInterval — человекочитаемое представление интервала.
func humanInterval(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		days := int(d / (24 * time.Hour))
		return fmt.Sprintf("%d дн", days)
	case d >= time.Hour:
		return fmt.Sprintf("%d ч", int(d/time.Hour))
	default:
		return fmt.Sprintf("%d мин", int(d/time.Minute))
	}
}
