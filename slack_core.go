package main

import (
	"fmt"
	"github.com/joomcode/errorx"
	"github.com/nlopes/slack"
	"go.uber.org/zap/zapcore"
	"sync"
	"sync/atomic"
	"time"
)

var (
	SlackTooManyFailures = errorx.ExternalError.New("Slack API max failures")
)

func NewSlackCore(hookURL string, encoder zapcore.Encoder, level zapcore.Level) *SlackCore {
	return &SlackCore{
		HookURL:       hookURL,
		AcceptedLevel: level,
		encoder:       encoder,
		entryChan:     make(chan *slack.WebhookMessage, 100),
		quitChan:      make(chan string),
		totalErrors:   0,
		failErrors:    10,
		wait:          new(sync.WaitGroup),
	}
}

// SlackCore is a zap Hook for dispatching messages to the specified
// channel on Slack.
type SlackCore struct {
	// Messages with a log level not contained in this array
	// will not be dispatched. If nil, all messages will be dispatched.
	AcceptedLevel zapcore.Level
	encoder       zapcore.Encoder
	HookURL       string // Webhook URL

	// slack post parameters
	Username  string // display name
	Channel   string // `#channel-name`
	IconEmoji string // emoji string ex) ":ghost:":
	IconURL   string // icon url

	FieldHeader string        // a header above field data
	Timeout     time.Duration // request timeout

	once sync.Once

	entryChan chan *slack.WebhookMessage
	quitChan  chan string
	wait      *sync.WaitGroup

	totalErrors int64
	failErrors  int64
}

func (sh *SlackCore) With(fields []zapcore.Field) zapcore.Core {
	clone := sh.clone()
	for _, field := range fields {
		field.AddTo(clone.encoder)
	}
	return clone
}

func (sh *SlackCore) Sync() error {
	sh.wait.Wait()
	return nil
}

func (sh *SlackCore) clone() *SlackCore {
	return &SlackCore{
		HookURL:       sh.HookURL,
		AcceptedLevel: sh.AcceptedLevel,
		encoder:       sh.encoder,
		entryChan:     make(chan *slack.WebhookMessage),
		quitChan:      make(chan string),
		wait:          new(sync.WaitGroup),
		totalErrors:   0,
		failErrors:    sh.failErrors,
	}
}

func (sh *SlackCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if sh.AcceptedLevel.Enabled(entry.Level) {
		return checked.AddCore(entry, sh)
	}
	return checked
}

func (sh *SlackCore) Enabled(lvl zapcore.Level) bool {
	return sh.AcceptedLevel.Enabled(lvl)
}

func (sh *SlackCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	sh.once.Do(func() {
		go sh.startWorker()
	})

	failures := atomic.LoadInt64(&sh.totalErrors)
	if failures >= sh.failErrors {
		return errorx.Decorate(SlackTooManyFailures, fmt.Sprintf("Reached maximum threshold of %d", failures))
	}

	// Generate the message.
	buffer, err := sh.encoder.EncodeEntry(entry, fields)
	if err != nil {
		return errorx.Decorate(err, "failed to encode log entry")
	}

	message := buffer.String()
	payload := createPayloadMessage(&entry, message)
	if entry.Level >= zapcore.FatalLevel {
		sh.wait.Wait()
		err := slack.PostWebhook(sh.HookURL, payload)
		return err
	}
	sh.entryChan <- payload
	sh.wait.Add(1)
	return nil
}

func (sh *SlackCore) GetHook() func(zapcore.Entry) error {
	return func(e zapcore.Entry) error {
		sh.once.Do(func() {
			go sh.startWorker()
		})
		if e.Level < sh.AcceptedLevel {
			return nil
		}
		payload := createPayload(&e)
		if e.Level == zapcore.PanicLevel {
			sh.Sync()
			err := slack.PostWebhook(sh.HookURL, payload)
			return err
		}
		sh.entryChan <- payload
		return nil
	}
}

func (sh *SlackCore) startWorker() error {
	for {
		select {
		case e := <-sh.entryChan:
			err := slack.PostWebhook(sh.HookURL, e)
			if err != nil {
				// fmt.Println(err)
				atomic.AddInt64(&sh.totalErrors, 1)
			}
			sh.wait.Done()
		case <-sh.quitChan:
			return nil
		}
	}
}

func createPayload(e *zapcore.Entry) *slack.WebhookMessage {
	color, _ := LevelColorMap[e.Level]

	attachment := slack.Attachment{}
	attachment.Text = e.Message
	attachment.Fallback = e.Message
	attachment.Color = color

	payload := slack.WebhookMessage{
		Attachments: []slack.Attachment{attachment},
	}
	return &payload
}

func createPayloadMessage(e *zapcore.Entry, message string) *slack.WebhookMessage {
	color, _ := LevelColorMap[e.Level]

	attachment := slack.Attachment{}
	attachment.Text = message
	attachment.Fallback = message
	attachment.Color = color

	payload := slack.WebhookMessage{
		Attachments: []slack.Attachment{attachment},
	}
	return &payload
}

var LevelColorMap = map[zapcore.Level]string{
	zapcore.DebugLevel: "#9B30FF",
	zapcore.InfoLevel:  "good",
	zapcore.WarnLevel:  "warning",
	zapcore.ErrorLevel: "danger",
	zapcore.FatalLevel: "danger",
	zapcore.PanicLevel: "danger",
}
