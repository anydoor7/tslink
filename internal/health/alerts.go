package health

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"time"

	"github.com/anydoor7/tslink/internal/atomicfile"
)

const ConfigFile = "alerts.json"
const StateFile = "health-alert-state.json"

// NotifierConfig is private input, never a public view. Command is an argv
// array, executed directly without a shell. Exactly one channel is allowed.
type NotifierConfig struct {
	Command []string `json:"command,omitempty"`
	Webhook string   `json:"webhook,omitempty"`
}

func LoadNotifier(path string) (NotifierConfig, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NotifierConfig{}, nil
	}
	if err != nil {
		return NotifierConfig{}, errors.New("alert_config_unreadable")
	}
	var c NotifierConfig
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF {
		return NotifierConfig{}, errors.New("alert_config_invalid")
	}
	if len(c.Command) != 0 && c.Webhook != "" {
		return NotifierConfig{}, errors.New("alert_config_invalid")
	}
	if len(c.Command) != 0 && !filepath.IsAbs(c.Command[0]) {
		return NotifierConfig{}, errors.New("alert_command_requires_absolute_path")
	}
	if c.Webhook != "" {
		u, err := url.Parse(c.Webhook)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.Fragment != "" {
			return NotifierConfig{}, errors.New("alert_webhook_invalid")
		}
	}
	return c, nil
}

func (c NotifierConfig) Kind() string {
	if len(c.Command) > 0 {
		return "command"
	}
	if c.Webhook != "" {
		return "webhook"
	}
	return "none"
}

type Event struct {
	ID       uint64    `json:"id"`
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	Service  string    `json:"service,omitempty"`
	Subject  string    `json:"subject,omitempty"`
	Health   *State    `json:"health,omitempty"`
	Expiry   *Expiry   `json:"expiry,omitempty"`
	Delivery string    `json:"delivery"`
}

type alertService struct {
	Identity string `json:"identity"`
	Health   State  `json:"health"`
	WasDown  bool   `json:"was_down"`
}
type expiryMark struct {
	Identity string `json:"identity"`
	Level    int    `json:"level"`
}
type AlertState struct {
	MonitorError       string                  `json:"monitor_error,omitempty"`
	Version            int                     `json:"version"`
	Services           map[string]alertService `json:"services"`
	Expiries           map[string]expiryMark   `json:"expiries"`
	LastDelivery       map[string]time.Time    `json:"last_delivery"`
	LastGlobalDelivery time.Time               `json:"last_global_delivery"`
	NextID             uint64                  `json:"next_id"`
	Events             []Event                 `json:"events"`
}
type AlertsView struct {
	MonitorError string  `json:"monitor_error,omitempty"`
	Notifier     string  `json:"notifier"`
	Destination  string  `json:"destination,omitempty"`
	Error        string  `json:"error,omitempty"`
	Events       []Event `json:"events"`
}

// Recorder is confined to one monitor goroutine. State is saved before any
// notifier invocation: restart never redelivers a committed event. Delivery is
// best effort and at most once, including a crash after saving but before send.
type Recorder struct {
	Path      string
	Config    NotifierConfig
	State     AlertState
	Error     string
	Send      func(context.Context, NotifierConfig, Event) error
	WriteFile func(string, []byte) error
	dirty     bool
	delivery  *deliveryWorker
}

func NewRecorder(path string, c NotifierConfig) *Recorder {
	r := &Recorder{Path: path, Config: c, Send: Notify, WriteFile: atomicfile.WriteFile}
	b, err := os.ReadFile(path)
	if err == nil {
		if json.Unmarshal(b, &r.State) != nil || r.State.Version != 1 {
			r.Error = "alert_state_invalid"
			r.State = AlertState{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		r.Error = "alert_state_unreadable"
	}
	r.State.Version = 1
	if r.State.Services == nil {
		r.State.Services = map[string]alertService{}
	}
	if r.State.Expiries == nil {
		r.State.Expiries = map[string]expiryMark{}
	}
	if r.State.LastDelivery == nil {
		r.State.LastDelivery = map[string]time.Time{}
	}
	return r
}

func (r *Recorder) View() AlertsView {
	v := AlertsView{Notifier: r.Config.Kind(), Error: r.Error, MonitorError: r.State.MonitorError, Events: append([]Event{}, r.State.Events...)}
	if v.Notifier != "none" {
		v.Destination = "[redacted]"
	}
	return v
}

func (r *Recorder) Previous(name, identity string) State {
	s := r.State.Services[name]
	if s.Identity != identity {
		return Unchecked("")
	}
	return s.Health
}

func (r *Recorder) ObserveHealth(name, identity string, h State, now time.Time) []Event {
	previous := r.State.Services[name]
	if previous.Identity != identity {
		previous = alertService{Identity: identity}
	}
	kind := ""
	if h.State == Down && !previous.WasDown {
		kind = "app_down"
		previous.WasDown = true
	}
	if h.State == Healthy && previous.WasDown {
		kind = "app_recovered"
		previous.WasDown = false
	}
	previous.Health = h
	if !reflect.DeepEqual(r.State.Services[name], previous) {
		r.dirty = true
	}
	r.State.Services[name] = previous
	if kind == "" {
		return nil
	}
	return []Event{{At: now.UTC(), Kind: kind, Service: name, Health: &h}}
}

func (r *Recorder) ObserveExpiry(service, subject string, e Expiry, now time.Time) []Event {
	if e.ExpiresAt == nil {
		return nil
	} // Unknown never erases a known crossing.
	key := service + "/" + subject
	identity := e.ExpiresAt.Format(time.RFC3339Nano) + "/" + e.Source
	mark := r.State.Expiries[key]
	if mark.Identity != identity {
		mark = expiryMark{Identity: identity}
	}
	level := map[string]int{"warning_14d": 1, "critical_3d": 2, "expired": 3}[e.Warning]
	if level <= mark.Level {
		return nil
	}
	mark.Level = level
	r.State.Expiries[key] = mark
	r.dirty = true
	return []Event{{At: now.UTC(), Kind: "expiry_threshold", Service: service, Subject: subject, Expiry: &e}}
}

// ObserveMonitor records one warning and one recovery per saturation episode.
// It is independent of backend health and notifier/journal errors.
func (r *Recorder) ObserveMonitor(saturated bool, now time.Time) []Event {
	code := ""
	kind := "monitor_recovered"
	if saturated {
		code = "health_monitor_saturated"
		kind = "monitor_saturated"
	}
	if r.State.MonitorError == code {
		return nil
	}
	r.State.MonitorError = code
	r.dirty = true
	return []Event{{At: now.UTC(), Kind: kind}}
}

// Commit records all transitions; only external delivery is rate limited.
// Per kind/subject: 5 minutes. Across all services: one delivery per minute.
func (r *Recorder) Commit(ctx context.Context, events []Event, now time.Time) bool {
	r.drainDelivery()
	changed := r.dirty || len(events) > 0
	r.dirty = changed
	available := 0
	if r.delivery != nil {
		available = cap(r.delivery.queue) - len(r.delivery.queue)
	}
	var send []int
	for _, e := range events {
		r.State.NextID++
		e.ID = r.State.NextID
		e.Delivery = "disabled"
		key := e.Service + "/" + e.Kind + "/" + e.Subject
		if r.Config.Kind() != "none" {
			e.Delivery = "rate_limited"
			last := r.State.LastDelivery[key]
			global := r.State.LastGlobalDelivery
			if (last.IsZero() || now.Sub(last) >= 5*time.Minute) && (global.IsZero() || now.Sub(global) >= time.Minute) {
				e.Delivery = "pending"
				r.State.LastDelivery[key] = now
				r.State.LastGlobalDelivery = now
				if r.delivery != nil && (available == 0 || ctx.Err() != nil) {
					e.Delivery = "failed"
				} else {
					send = append(send, len(r.State.Events))
					available--
				}
			}
		}
		r.State.Events = append(r.State.Events, e)
	}
	// Bound the on-disk journal; never retain response/config secrets.
	if len(r.State.Events) > 100 {
		drop := len(r.State.Events) - 100
		r.State.Events = r.State.Events[drop:]
		for i := range send {
			send[i] -= drop
		}
	}
	if r.save() != nil {
		r.Error = "alert_state_write_failed"
		return true
	}
	for _, i := range send {
		if i < 0 {
			continue
		}
		e := &r.State.Events[i]
		if r.delivery != nil {
			// Only this owner enqueues. The capacity reserved above cannot shrink
			// before enqueue, and the pending reservation is already durable.
			r.delivery.queue <- *e
			continue
		}

		if r.Send(ctx, r.Config, *e) != nil || ctx.Err() != nil {
			e.Delivery = "failed"
		} else {
			e.Delivery = "sent"
		}
	}
	if r.delivery == nil && len(send) > 0 {
		r.dirty = true
		if r.save() != nil {
			r.Error = "alert_state_write_failed"
		}
	}
	return changed
}

func (r *Recorder) save() error {
	if !r.dirty {
		return nil
	}
	b, err := json.Marshal(r.State)
	if err != nil {
		return err
	}
	err = r.WriteFile(r.Path, append(b, '\n'))
	if err == nil {
		r.dirty = false
	}
	return err
}

// Notify deliberately discards command output and webhook bodies. Errors never
// wrap os/exec or net/http errors, whose messages may contain secrets.
func Notify(ctx context.Context, c NotifierConfig, e Event) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	b, err := json.Marshal(e)
	if err != nil {
		return errors.New("alert_payload_invalid")
	}
	switch c.Kind() {
	case "command":
		cmd := exec.CommandContext(ctx, c.Command[0], c.Command[1:]...)
		cmd.WaitDelay = 250 * time.Millisecond
		cleanup := configureNotifierCommand(cmd)
		defer cleanup()
		cmd.Env = append(os.Environ(), "TSLINK_ALERT_KIND="+e.Kind, "TSLINK_ALERT_SERVICE="+e.Service, "TSLINK_ALERT_JSON="+string(b))
		cmd.Stdin = bytes.NewReader(append(b, '\n'))
		// Nil stdout/stderr connect directly to the null device: descendants
		// cannot hold an os/exec output-copy pipe open after the parent exits.
		if cmd.Run() != nil || ctx.Err() != nil {
			return errors.New("alert_command_failed")
		}
	case "webhook":
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Webhook, bytes.NewReader(b))
		if err != nil {
			return errors.New("alert_webhook_failed")
		}
		req.Header.Set("Content-Type", "application/json")
		transport := &http.Transport{}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			return errors.New("alert_webhook_failed")
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 || ctx.Err() != nil {
			return errors.New("alert_webhook_failed")
		}
	}
	return nil
}
