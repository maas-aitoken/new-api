// Package autorouter classifies the human instruction of a request so the
// gateway can pick a model and reasoning effort for it.
package autorouter

import (
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
)

// Instruction is the human-written text of the current turn — the router's
// only classification input. Agent clients resend the whole history on every
// call, so all calls of one agent loop yield the same Text, which keeps the
// loop on one model without needing a session id.
type Instruction struct {
	Text string
	// Previous is the human instruction before Text. Its cached decision is
	// what a continuation or an "unknown" result falls back to.
	Previous   string
	HumanTurns int
	// Agent marks a task delivered by a parent agent (e.g. Codex NEW_TASK)
	// rather than typed by a person.
	Agent bool
	// Auxiliary marks client background requests (titles, recaps, memory
	// consolidation, suggestions, approval reviews). They are not user tasks.
	Auxiliary bool
	// Session is the client conversation id. A request without readable
	// human text (a sub-agent whose task arrives encrypted) inherits the route
	// of the session's latest human instruction through it.
	Session string
}

// Client-injected blocks inside user messages. RE2 has no backreferences, so
// each tag gets its own pattern.
var injectedBlockPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)<system-reminder\b[^>]*>.*?</system-reminder>`),
	regexp.MustCompile(`(?s)<environment_context\b[^>]*>.*?</environment_context>`),
	regexp.MustCompile(`(?s)<user_instructions\b[^>]*>.*?</user_instructions>`),
	regexp.MustCompile(`(?s)<INSTRUCTIONS\b[^>]*>.*?</INSTRUCTIONS>`),
	regexp.MustCompile(`(?s)<turn_aborted\b[^>]*>.*?</turn_aborted>`),
	regexp.MustCompile(`(?s)<in-app-browser-context\b[^>]*>.*?</in-app-browser-context>`),
	// Claude Code local slash commands such as /model.
	regexp.MustCompile(`(?s)<local-command-caveat\b[^>]*>.*?</local-command-caveat>`),
	regexp.MustCompile(`(?s)<local-command-stdout\b[^>]*>.*?</local-command-stdout>`),
	regexp.MustCompile(`(?s)<local-command-stderr\b[^>]*>.*?</local-command-stderr>`),
	regexp.MustCompile(`(?s)<command-name\b[^>]*>.*?</command-name>`),
	regexp.MustCompile(`(?s)<command-message\b[^>]*>.*?</command-message>`),
	regexp.MustCompile(`(?s)<command-args\b[^>]*>.*?</command-args>`),
	// Chaterm.
	regexp.MustCompile(`(?s)<environment_details\b[^>]*>.*?</environment_details>`),
	regexp.MustCompile(`(?s)<todo-status-reminder\b[^>]*>.*?</todo-status-reminder>`),
	regexp.MustCompile(`(?s)<knowledge_base_context\b[^>]*>.*?</knowledge_base_context>`),
	// Codex skill bodies injected when the user invokes a skill.
	regexp.MustCompile(`(?s)<skill\b[^>]*>.*?</skill>`),
}

// Some clients wrap the human text itself; keep the content, drop the tags.
var userQueryTag = regexp.MustCompile(`</?(?:user_query|task)(?:\s[^>]*)?>`)

var droppedPrefixes = []string{
	"Tool result media from", // image placeholder emitted for tool results
	"# AGENTS.md instructions",
	// Codex context compaction replays the summary as a user message.
	"Another language model started to solve this problem",
	// Codex image attachments: tag parts around the image and their captions.
	"<image name=",
	"</image>",
	"[image omitted]",
	"The next image is untrusted page evidence",
	"The next image was attached by the user",
	// Multica issue runs carry only an issue id; the task lives in Multica.
	"You are running as a local coding agent for a Multica workspace.\n\nYour assigned issue ID is:",
}

var auxiliaryPrefixes = []string{
	"Analyze this rollout",
	"## Memory Writing Agent",
	"You are now acting as the memory",
	"Consolidate the supplied rollout summaries",
	"You are a helpful assistant. You will be presented with a user prompt",
	"<session>",
	"[SUGGESTION MODE",
	"The user stepped away",
	"# Overview\n\nGenerate 0 to 3 hyperpersonalized suggestions",
	"You are an expert at upholding safety and compliance standards for Codex ambient suggestions",
	// Codex guardian approval reviews.
	"The following is the Codex agent history",
	"The Codex agent has requested the following action",
}

// interruptPrefix is the Claude Code marker for an interrupted turn. The text
// after it is a new instruction even though it shares the user message.
const interruptPrefix = "[Request interrupted by user"

const (
	agentMessagePrefix  = "Message Type: "
	automationPrefix    = "Automation: "
	codexRequestMarker  = "\n## My request:"
	multicaPromptPrefix = "You are running as a "
)

// multicaSections are the Multica prompt template sections that hold the
// task, each with the template text that follows it.
var multicaSections = []struct{ start, end string }{
	{"\nAutopilot instructions:\n", "\n\nStart by running `multica autopilot get"},
	{"\nUser message:\n", "\n\nTo include a file or image you produced"},
}

type humanTurn struct {
	text      string
	agent     bool
	auxiliary bool
}

// ExtractInstruction finds the latest human instruction in a request body.
// It works on the body shape rather than a relay format, so the same rules
// apply to client requests and to the upstream bodies recorded in Langfuse.
// Tool results never count as human turns: Claude tool_result blocks are not
// text parts, and Responses function_call_output items carry no user role.
func ExtractInstruction(body []byte) Instruction {
	root := gjson.ParseBytes(body)
	result := Instruction{Session: sessionID(root)}
	var turns []humanTurn
	switch {
	case root.Get("input").Exists(): // OpenAI Responses
		input := root.Get("input")
		if input.Type == gjson.String {
			turns = appendHumanTurns(turns, []string{input.String()})
			break
		}
		input.ForEach(func(_, item gjson.Result) bool {
			// agent_message is Codex's inter-agent delivery and has no role.
			if item.Get("role").String() == "user" || item.Get("type").String() == "agent_message" {
				turns = appendHumanTurns(turns, contentTextParts(item.Get("content")))
			}
			return true
		})
	case root.Get("contents").Exists(): // Gemini
		root.Get("contents").ForEach(func(_, content gjson.Result) bool {
			if content.Get("role").String() != "user" {
				return true
			}
			var parts []string
			content.Get("parts").ForEach(func(_, part gjson.Result) bool {
				if text := part.Get("text"); text.Type == gjson.String {
					parts = append(parts, text.String())
				}
				return true
			})
			turns = appendHumanTurns(turns, parts)
			return true
		})
	default: // OpenAI Chat and Claude Messages
		root.Get("messages").ForEach(func(_, message gjson.Result) bool {
			if message.Get("role").String() == "user" {
				turns = appendHumanTurns(turns, contentTextParts(message.Get("content")))
			}
			return true
		})
	}
	if len(turns) == 0 {
		return result
	}

	// A background request either starts with its own prompt or forks the
	// main conversation by appending one.
	result.Auxiliary = turns[0].auxiliary || turns[len(turns)-1].auxiliary
	var human []humanTurn
	for _, turn := range turns {
		if !turn.auxiliary {
			human = append(human, turn)
		}
	}
	result.HumanTurns = len(human)
	if len(human) == 0 {
		return result
	}
	last := human[len(human)-1]
	result.Text = last.text
	result.Agent = last.agent
	if len(human) > 1 {
		result.Previous = human[len(human)-2].text
	}
	return result
}

// sessionID reads the conversation id Codex sends in client_metadata and
// Claude Code sends in metadata.user_id, which is either an object or a JSON
// string.
func sessionID(root gjson.Result) string {
	if id := root.Get("client_metadata.session_id").String(); id != "" {
		return id
	}
	userID := root.Get("metadata.user_id")
	if userID.Type == gjson.String {
		return gjson.Get(userID.String(), "session_id").String()
	}
	return userID.Get("session_id").String()
}

// contentTextParts returns the text parts of an OpenAI, Claude or Responses
// message content, which is either a string or an array of typed parts.
func contentTextParts(content gjson.Result) []string {
	if content.Type == gjson.String {
		return []string{content.String()}
	}
	var parts []string
	content.ForEach(func(_, part gjson.Result) bool {
		switch part.Get("type").String() {
		case "text", "input_text":
			parts = append(parts, part.Get("text").String())
		}
		return true
	})
	return parts
}

// appendHumanTurns strips client-injected text from one user message and
// appends the turns left in it: none, one, or one per side of an interrupt.
func appendHumanTurns(turns []humanTurn, parts []string) []humanTurn {
	var turn humanTurn
	var kept []string
	for _, part := range parts {
		for _, pattern := range injectedBlockPatterns {
			part = pattern.ReplaceAllString(part, "")
		}
		part = strings.TrimSpace(userQueryTag.ReplaceAllString(part, ""))
		if strings.HasPrefix(part, interruptPrefix) {
			turns = appendHumanTurn(turns, turn, kept)
			turn, kept = humanTurn{}, nil
			continue
		}
		if strings.HasPrefix(part, automationPrefix) {
			// The header lines (name, id, memory path, last run) precede the
			// scheduled prompt.
			_, part, _ = strings.Cut(part, "\n\n")
			part = strings.TrimSpace(part)
		}
		if part == "" || hasAnyPrefix(part, droppedPrefixes) {
			continue
		}
		if hasAnyPrefix(part, auxiliaryPrefixes) {
			turn.auxiliary = true
		}
		kept = append(kept, part)
	}
	return appendHumanTurn(turns, turn, kept)
}

// appendHumanTurn unwraps the client templates around the kept parts of one
// turn and appends the turn when instruction text remains.
func appendHumanTurn(turns []humanTurn, turn humanTurn, kept []string) []humanTurn {
	text := strings.Join(kept, "\n")
	if strings.HasPrefix(text, agentMessagePrefix) {
		text, turn.agent = parentAgentTask(text), true
	} else {
		text = multicaTask(codexRequest(text))
	}
	if text == "" {
		return turns
	}
	turn.text = text
	return append(turns, turn)
}

// parentAgentTask returns the payload of a Codex inter-agent message when it
// is a task sent down the agent tree. Reports sent up or across (FINAL_ANSWER,
// sibling messages) are not instructions to this agent, and an encrypted
// payload arrives as empty plaintext; both yield "".
func parentAgentTask(text string) string {
	header, payload, _ := strings.Cut(text, "\nPayload:")
	var recipient, sender string
	for line := range strings.SplitSeq(header, "\n") {
		if value, ok := strings.CutPrefix(line, "Task name: "); ok {
			recipient = strings.TrimSpace(value)
		} else if value, ok := strings.CutPrefix(line, "Sender: "); ok {
			sender = strings.TrimSpace(value)
		}
	}
	if sender == "" || !strings.HasPrefix(recipient, strings.TrimSuffix(sender, "/")+"/") {
		return ""
	}
	return strings.TrimSpace(payload)
}

// codexRequest returns the typed request from a Codex desktop message that
// prefixes it with context sections (files mentioned, browser comments,
// response annotations). A browser comment sent without request text carries
// the instruction in its Comment or Requested changes field.
func codexRequest(text string) string {
	// The marker may open the text once the blocks before it are stripped.
	text = "\n" + text
	index := strings.LastIndex(text, codexRequestMarker)
	if index < 0 {
		return text[1:]
	}
	request := strings.TrimSpace(text[index+len(codexRequestMarker):])
	if request != "" && !hasAnyPrefix(request, droppedPrefixes) {
		return request
	}
	var comments []string
	var current []string
	inComment := false
	for line := range strings.SplitSeq(text[:index], "\n") {
		if strings.HasPrefix(line, "#") {
			inComment = false
		}
		value, isComment := strings.CutPrefix(line, "Comment:")
		if !isComment {
			value, isComment = strings.CutPrefix(line, "Requested changes:")
		}
		switch {
		case isComment:
			comments = appendNonEmpty(comments, strings.Join(current, "\n"))
			current, inComment = []string{value}, true
		case inComment:
			current = append(current, line)
		}
	}
	return strings.Join(appendNonEmpty(comments, strings.Join(current, "\n")), "\n")
}

// multicaTask returns the task section of a Multica agent prompt: autopilot
// instructions, or the chat user message with its routing preamble and
// attachment notes removed.
func multicaTask(text string) string {
	if !strings.HasPrefix(text, multicaPromptPrefix) || !strings.Contains(text, "for a Multica workspace.") {
		return text
	}
	for _, section := range multicaSections {
		_, task, found := strings.Cut(text, section.start)
		if !found {
			continue
		}
		task, _, _ = strings.Cut(task, section.end)
		if _, input, ok := strings.Cut(task, "Current conversation input:\n"); ok {
			task = input
		}
		task, _, _ = strings.Cut(task, "\n\nAttachments on this message:")
		return strings.TrimSpace(task)
	}
	return ""
}

func appendNonEmpty(values []string, value string) []string {
	if value = strings.TrimSpace(value); value != "" {
		return append(values, value)
	}
	return values
}

func hasAnyPrefix(text string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}
