package main

import (
	"strings"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
	"github.com/oneo-me/nsgo/ui/chat"
)

// dictation is the composer's microphone: NSGo's speech session writes what
// the user says into the draft while the recognizer listens. The transcript
// replaces the dictation's own tail only, so text typed (or inserted by a
// mention) before it survives every interim result.
type dictation struct {
	session *core.SpeechSession
	base    string
}

// toggleDictation starts or stops listening. A second press while the
// recognizer runs stops it, which keeps the transcript it has.
func (s *appState) toggleDictation() {
	if s.dictation != nil {
		if session := s.dictation.session; session != nil && !session.Ended() {
			session.Stop()
			return
		}
		s.dictation = nil
		s.dictating.Set(false)
		return
	}
	base := s.draft.GetUntracked()
	d := &dictation{base: base}
	session, err := s.cx.StartSpeech(core.SpeechOptions{
		Interim: true,
		OnStart: func() { s.dictating.Set(true) },
		OnResult: func(text string, final bool) {
			s.draft.Set(joinDictation(d.base, text))
		},
		OnEnd: func(text string, err error) {
			s.dictating.Set(false)
			s.dictation = nil
			if err != nil && text == "" {
				s.notify(ui.ToastWarning, "语音输入不可用", speechReason(err))
			}
		},
	})
	if err != nil {
		s.notify(ui.ToastWarning, "无法开始语音输入", err.Error())
		return
	}
	d.session = session
	s.dictation = d
	s.dictating.Set(true)
}

// joinDictation appends a transcript to what was in the field before it, with
// one space between them and none before punctuation.
func joinDictation(base, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return base
	}
	if base == "" {
		return text
	}
	if strings.HasSuffix(base, " ") || strings.HasSuffix(base, "\n") {
		return base + text
	}
	return base + " " + text
}

// speechReason names the common ways a speech session ends without words.
func speechReason(err error) string {
	if err == core.SpeechNotAllowed {
		return "系统拒绝了麦克风或语音识别权限，可在「系统设置 → 隐私与安全性」里打开"
	}
	return err.Error()
}

// dictationAction is the composer's microphone button, lit while it listens.
func (s *appState) dictationAction() core.Node {
	return chat.IconAction{
		Label:  "语音输入",
		Icon:   lucide.Mic,
		TestID: "dictate",
		Active: s.dictating.Get,
		OnPress: func() {
			s.toggleDictation()
		},
	}
}
