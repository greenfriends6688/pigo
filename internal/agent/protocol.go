// Package agent owns the JSONL RPC transport, client and session pool used to
// drive pi's `--mode rpc` subprocess. Only the Go standard library is used.
//
// Wire framing is LF-only (see jsonl.js in pi's rpc mode): records are split on
// the byte '\n' and never on Unicode separators such as U+2028/U+2029, which
// are legal inside JSON strings. Responses and events share stdout, so callers
// route frames by content rather than by arrival order.
package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Command is a single RPC command object. It is serialized as one JSON line on
// the child process' stdin.
type Command map[string]any

// Frame is a parsed JSONL frame emitted by the RPC process. Raw holds every
// field of the original object so the chat reducer can read protocol fields
// that are not surfaced as typed members.
type Frame struct {
	ID, Type, Command, Error string
	Success                  bool
	Data                     json.RawMessage
	Raw                      map[string]json.RawMessage
}

// Frame type values carried in Frame.Type.
const (
	// FrameResponse marks a command response; events use their own Ev* names.
	FrameResponse = "response"
)

// The 33 command names of the RpcCommand union (rpc-types.d.ts). These are the
// `type` values written to stdin.
const (
	CmdPrompt                     = "prompt"
	CmdSteer                      = "steer"
	CmdFollowUp                   = "follow_up"
	CmdAbort                      = "abort"
	CmdClearQueue                 = "clear_queue"
	CmdNewSession                 = "new_session"
	CmdGetState                   = "get_state"
	CmdSetModel                   = "set_model"
	CmdCycleModel                 = "cycle_model"
	CmdGetAvailableModels         = "get_available_models"
	CmdSetThinkingLevel           = "set_thinking_level"
	CmdCycleThinkingLevel         = "cycle_thinking_level"
	CmdGetAvailableThinkingLevels = "get_available_thinking_levels"
	CmdSetSteeringMode            = "set_steering_mode"
	CmdSetFollowUpMode            = "set_follow_up_mode"
	CmdCompact                    = "compact"
	CmdSetAutoCompaction          = "set_auto_compaction"
	CmdSetAutoRetry               = "set_auto_retry"
	CmdAbortRetry                 = "abort_retry"
	CmdBash                       = "bash"
	CmdAbortBash                  = "abort_bash"
	CmdGetSessionStats            = "get_session_stats"
	CmdExportHTML                 = "export_html"
	CmdSwitchSession              = "switch_session"
	CmdFork                       = "fork"
	CmdClone                      = "clone"
	CmdGetForkMessages            = "get_fork_messages"
	CmdGetEntries                 = "get_entries"
	CmdGetTree                    = "get_tree"
	CmdGetLastAssistantText       = "get_last_assistant_text"
	CmdSetSessionName             = "set_session_name"
	CmdGetMessages                = "get_messages"
	CmdGetCommands                = "get_commands"
)

// CmdExtensionUIResponse is not part of RpcCommand but is a valid stdin frame:
// it answers a pending extension_ui_request and produces no response frame.
const CmdExtensionUIResponse = "extension_ui_response"

// Event names emitted on stdout. Most come from AgentSessionEvent via
// toJsonEvent; bash_output and extension_ui_closed are synthesized by the host.
const (
	EvAgentStart          = "agent_start"
	EvAgentEnd            = "agent_end"
	EvMessageStart        = "message_start"
	EvMessageUpdate       = "message_update"
	EvMessageEnd          = "message_end"
	EvToolExecutionStart  = "tool_execution_start"
	EvToolExecutionUpdate = "tool_execution_update"
	EvToolExecutionEnd    = "tool_execution_end"
	EvQueueUpdate         = "queue_update"
	EvAutoRetryStart      = "auto_retry_start"
	EvAutoRetryEnd        = "auto_retry_end"
	EvCompactionStart     = "compaction_start"
	EvCompactionEnd       = "compaction_end"
	EvBashOutput          = "bash_output"
	EvExtensionUIRequest  = "extension_ui_request"
	EvExtensionError      = "extension_error"
	EvExtensionUIClosed   = "extension_ui_closed"
)

// Field-name constants are the verbatim JSON keys used on the wire. The reducer
// indexes Frame.Raw with these instead of repeating string literals. Values are
// taken from rpc-types.d.ts, json-event.js and the AgentEvent emitters.
const (
	FType    = "type"
	FID      = "id"
	FCommand = "command"
	FSuccess = "success"
	FError   = "error"
	FData    = "data"

	FMessage            = "message"
	FImages             = "images"
	FStreamingBehavior  = "streamingBehavior"
	FParentSession      = "parentSession"
	FProvider           = "provider"
	FModelId            = "modelId"
	FCustomInstructions = "customInstructions"
	FOutputPath         = "outputPath"
	FExcludeFromContext = "excludeFromContext"
	FSince              = "since"

	FToolCallId            = "toolCallId"
	FToolName              = "toolName"
	FArgs                  = "args"
	FParentToolCallId      = "parentToolCallId"
	FAssistantMessageEvent = "assistantMessageEvent"
	FTextDelta             = "delta"
	FText                  = "text"
	FSteering              = "steering"
	FFollowUp              = "followUp"
	FSessionPath           = "sessionPath"
	FName                  = "name"
	FEntryId               = "entryId"
	FEnabled               = "enabled"
	FMode                  = "mode"
	FLevel                 = "level"

	FDisposition   = "disposition"
	FValue         = "value"
	FConfirmed     = "confirmed"
	FCancelled     = "cancelled"
	FMethod        = "method"
	FTitle         = "title"
	FOptions       = "options"
	FTimeout       = "timeout"
	FPlaceholder   = "placeholder"
	FPrefill       = "prefill"
	FUsage         = "usage"
	FContentIndex  = "contentIndex"
	FPartial       = "partial"
	FPartialResult = "partialResult"
	FIsError       = "isError"
	FResult        = "result"
	FReason        = "reason"
	FAttempt       = "attempt"
	FMaxAttempts   = "maxAttempts"
	FDelayMs       = "delayMs"
	FErrorMessage  = "errorMessage"
	FWillRetry     = "willRetry"
	FAborted       = "aborted"
	FExtensionPath = "extensionPath"
	FEvent         = "event"
	FOutput        = "output"
)

// ErrEmptyLine is returned by ParseFrame for a blank or whitespace-only line.
var ErrEmptyLine = errors.New("agent: empty JSONL line")

// ParseFrame parses one JSONL line into a Frame. A single trailing carriage
// return is stripped (LF framing tolerates CRLF), blank lines yield
// ErrEmptyLine, and malformed JSON yields a wrapped error. Raw always contains
// the full decoded object.
func ParseFrame(line []byte) (Frame, error) {
	line = bytes.TrimSuffix(line, []byte("\r"))
	if len(bytes.TrimSpace(line)) == 0 {
		return Frame{}, ErrEmptyLine
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return Frame{}, fmt.Errorf("agent: invalid frame JSON: %w", err)
	}

	f := Frame{Raw: raw}
	f.ID = rawString(raw, FID)
	f.Type = rawString(raw, FType)
	f.Command = rawString(raw, FCommand)
	f.Error = rawString(raw, FError)
	f.Success = rawBool(raw, FSuccess)
	f.Data = raw[FData]
	return f, nil
}

// SplitLines reads LF-framed JSONL records from r and sends each line to out,
// without its trailing '\n' and without a single trailing '\r'. It splits on the
// byte 0x0A exclusively, so U+2028/U+2029 and UTF-8 sequences spanning chunk
// boundaries are preserved. It never closes out; the caller owns that. A clean
// EOF (including a final unterminated line) is not an error; other read errors
// are returned.
func SplitLines(r io.Reader, out chan<- []byte) error {
	return splitLinesWithStop(r, out, nil)
}

// splitLinesWithStop is SplitLines with a shutdown signal. Once stop closes,
// frames are dropped but stdout keeps being drained until EOF: the child can
// flush its last writes instead of dying on EPIPE, and the read loop cannot
// deadlock on a full frames channel after the Client's dispatch goroutine has
// gone. A blocked send was the reason Close() used to reach its 5s deadline
// and SIGKILL the whole process group, which showed up as "node 被杀掉".
func splitLinesWithStop(r io.Reader, out chan<- []byte, stop <-chan struct{}) error {
	br := bufio.NewReaderSize(r, 1<<20)
	dropping := false
	for {
		line, err := br.ReadBytes('\n')
		if !dropping && stop != nil {
			select {
			case <-stop:
				dropping = true
			default:
			}
		}
		if len(line) > 0 && !dropping {
			line = bytes.TrimSuffix(line, []byte("\n"))
			line = bytes.TrimSuffix(line, []byte("\r"))
			if stop == nil {
				out <- line
			} else {
				select {
				case out <- line:
				case <-stop:
					dropping = true
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func rawString(m map[string]json.RawMessage, key string) string {
	raw, ok := m[key]
	if !ok || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

func rawBool(m map[string]json.RawMessage, key string) bool {
	raw, ok := m[key]
	if !ok || string(raw) == "null" {
		return false
	}
	var b bool
	if json.Unmarshal(raw, &b) != nil {
		return false
	}
	return b
}

// ConfirmResponse builds the `{value?}`-free confirmed/cancelled shape of
// RpcExtensionUIResponse for a confirm dialog.
func ConfirmResponse(id string, ok bool) Command {
	return Command{
		FType:      CmdExtensionUIResponse,
		FID:        id,
		FConfirmed: ok,
	}
}

// ValueResponse builds the `{value}` shape of RpcExtensionUIResponse for
// select/input/editor dialogs.
func ValueResponse(id, value string) Command {
	return Command{
		FType:  CmdExtensionUIResponse,
		FID:    id,
		FValue: value,
	}
}

// CancelResponse builds the `{cancelled: true}` shape of RpcExtensionUIResponse.
func CancelResponse(id string) Command {
	return Command{
		FType:      CmdExtensionUIResponse,
		FID:        id,
		FCancelled: true,
	}
}
