// Package scheduler — фоновая горутина, которая опрашивает Google Calendar
// и отправляет напоминания родителям через notify.Dispatcher.
//
// Логика:
//  1. Тик каждые SchedulerTick (по умолчанию 5 мин).
//  2. Получаем instance события на окно [now, now + maxInterval + slack].
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
	to := now.Add(maxInt + 10*time.Minute)

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

	if in.Status == calendar.StatusCancelled {
		log.Debug("пропуск cancelled", "instance_id", in.ID)
		return
	}

	student, err := s.store.GetStudentForEvent(ctx, in.MasterID)
	if errors.Is(err, store.ErrNotFound) {
		log.Debug("событие без ученика", "instance_id", in.ID, "master_id", in.MasterID)
		return
	}
	if err != nil {
		log.Error("GetStudentForEvent", "error", err)
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
	when := start.Local().Format("Mon 02.01 в 15:04")
	return fmt.Sprintf(
		"Напоминание: занятие у %s\n%s\nЧерез %s (%s)",
		student, summary, humanInterval(interval), when,
	)
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
