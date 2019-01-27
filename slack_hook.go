package main

import (
	"errors"
	"github.com/nlopes/slack"
	"go.uber.org/zap/zapcore"
	"sync"
	"time"
)

func NewSlackHook(hookURL string, level zapcore.Level) *SlackHook {
	return &SlackHook{
		HookURL:       hookURL,
		AcceptedLevel: level,
		entryChan:     make(chan zapcore.Entry),
		quitChan:      make(chan string),
	}
}

// SlackHook is a zap Hook for dispatching messages to the specified
// channel on Slack.
type SlackHook struct {
	// Messages with a log level not contained in this array
	// will not be dispatched. If nil, all messages will be dispatched.
	AcceptedLevel zapcore.Level
	HookURL       string // Webhook URL

	// slack post parameters
	Username  string // display name
	Channel   string // `#channel-name`
	IconEmoji string // emoji string ex) ":ghost:":
	IconURL   string // icon url

	FieldHeader string        // a header above field data
	Timeout     time.Duration // request timeout

	once sync.Once

	entryChan chan zapcore.Entry
	quitChan  chan string
}

func (sh *SlackHook) GetHook() func(zapcore.Entry) error {
	return func(e zapcore.Entry) error {
		sh.once.Do(func() {
			go sh.startWorker()
		})
		if e.Level < sh.AcceptedLevel {
			return nil
		}
		if e.Level == zapcore.PanicLevel {
			err := slack.PostWebhook(sh.HookURL, createPayload(&e))
			return err
		}
		sh.entryChan <- e
		return nil
	}
}

func (sh *SlackHook) startWorker() error {
	for {
		select {
		case e := <-sh.entryChan:
			err := slack.PostWebhook(sh.HookURL, createPayload(&e))
			if err != nil {
				panic(err)
			}
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

var LevelColorMap = map[zapcore.Level]string{
	zapcore.DebugLevel: "#9B30FF",
	zapcore.InfoLevel:  "good",
	zapcore.WarnLevel:  "warning",
	zapcore.ErrorLevel: "danger",
	zapcore.FatalLevel: "danger",
	zapcore.PanicLevel: "danger",
}

var TimeoutError = errors.New("Request timed out")
