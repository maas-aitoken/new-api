package autorouter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractInstruction(t *testing.T) {
	tests := []struct {
		name string
		body string
		want Instruction
	}{
		{
			name: "openai chat skips tool messages and keeps previous turn",
			body: `{"messages":[{"role":"system","content":"sys"},{"role":"user","content":"修复登录 bug"},{"role":"assistant","content":null,"tool_calls":[{"id":"1"}]},{"role":"tool","content":"ok"},{"role":"user","content":"再加个单测"}]}`,
			want: Instruction{Text: "再加个单测", Previous: "修复登录 bug", HumanTurns: 2},
		},
		{
			name: "claude tool_result-only turn is not a human turn",
			body: `{"system":"sys","messages":[{"role":"user","content":[{"type":"text","text":"重构这个模块"}]},{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"file body"}]}]}`,
			want: Instruction{Text: "重构这个模块", HumanTurns: 1},
		},
		{
			name: "claude code system-reminder blocks are stripped",
			body: `{"messages":[{"role":"user","content":[{"type":"text","text":"<system-reminder>\nCodebase instructions\n</system-reminder>"},{"type":"text","text":"<system-reminder data-role=\"user-context\">ctx</system-reminder>\n帮我看下这个报错"}]}]}`,
			want: Instruction{Text: "帮我看下这个报错", HumanTurns: 1},
		},
		{
			name: "codex responses drops environment and AGENTS.md messages",
			body: `{"instructions":"sys","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /repo\n\n<INSTRUCTIONS>rules</INSTRUCTIONS>"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n<cwd>/repo</cwd>\n</environment_context>"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"检查一下是不是没安装成功"}]},{"type":"function_call","call_id":"c1","name":"exec","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":"done"}]}`,
			want: Instruction{Text: "检查一下是不是没安装成功", HumanTurns: 1},
		},
		{
			name: "responses string input",
			body: `{"input":"总结这篇文章"}`,
			want: Instruction{Text: "总结这篇文章", HumanTurns: 1},
		},
		{
			name: "user_query wrapper keeps its content",
			body: `{"messages":[{"role":"user","content":"<system-reminder data-role=\"user-context\">info</system-reminder>\n<user_query>你是什么模型</user_query>"}]}`,
			want: Instruction{Text: "你是什么模型", HumanTurns: 1},
		},
		{
			name: "tool result image placeholder is dropped",
			body: `{"messages":[{"role":"user","content":"截图里的按钮对齐了吗"},{"role":"user","content":[{"type":"text","text":"Tool result media from Read:"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA"}}]}]}`,
			want: Instruction{Text: "截图里的按钮对齐了吗", HumanTurns: 1},
		},
		{
			name: "memory fork appended to main conversation is auxiliary",
			body: `{"messages":[{"role":"user","content":"列出 top 20 用户"},{"role":"assistant","content":"..."},{"role":"user","content":"You are now acting as the memory extraction subagent."}]}`,
			want: Instruction{Text: "列出 top 20 用户", HumanTurns: 1, Auxiliary: true},
		},
		{
			name: "background request that starts with its own prompt is auxiliary",
			body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Analyze this rollout and produce JSON"}]}]}`,
			want: Instruction{Auxiliary: true},
		},
		{
			name: "codex plaintext task from parent agent is the agent's instruction",
			body: `{"input":[{"type":"agent_message","content":[{"type":"input_text","text":"Message Type: MESSAGE\nTask name: /root/scan\nSender: /root\nPayload:\n扫描 relay 目录下所有超时配置"}]}]}`,
			want: Instruction{Text: "扫描 relay 目录下所有超时配置", HumanTurns: 1, Agent: true},
		},
		{
			name: "codex forked sub-agent with encrypted task keeps the parent instruction",
			body: `{"client_metadata":{"session_id":"s-1"},"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"排查网关 502"}]},{"type":"agent_message","content":[{"type":"input_text","text":"Message Type: NEW_TASK\nTask name: /root/scan\nSender: /root\nPayload:"},{"type":"encrypted_content","encrypted_content":"gAAA"}]}]}`,
			want: Instruction{Text: "排查网关 502", HumanTurns: 1, Session: "s-1"},
		},
		{
			name: "codex sub-agent with only an encrypted task has no text but a session",
			body: `{"client_metadata":{"session_id":"s-1"},"input":[{"type":"agent_message","content":[{"type":"input_text","text":"Message Type: NEW_TASK\nTask name: /root/scan\nSender: /root\nPayload:"},{"type":"encrypted_content","encrypted_content":"gAAA"}]}]}`,
			want: Instruction{Session: "s-1"},
		},
		{
			name: "codex sub-agent report to the root is not an instruction",
			body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"排查网关 502"}]},{"type":"agent_message","content":[{"type":"input_text","text":"Message Type: FINAL_ANSWER\nTask name: /root\nSender: /root/scan\nPayload:\n找到 3 处超时配置"}]}]}`,
			want: Instruction{Text: "排查网关 502", HumanTurns: 1},
		},
		{
			name: "codex guardian approval review is auxiliary",
			body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"The following is the Codex agent history whose request action you are assessing.\n>>> TRANSCRIPT START\n[1] user: 删掉 build 目录"}]}]}`,
			want: Instruction{Auxiliary: true},
		},
		{
			name: "codex memory consolidation is auxiliary",
			body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Consolidate the supplied rollout summaries into memory."}]}]}`,
			want: Instruction{Auxiliary: true},
		},
		{
			name: "codex ambient suggestions are auxiliary",
			body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"# Overview\n\nGenerate 0 to 3 hyperpersonalized suggestions for the user."}]}]}`,
			want: Instruction{Auxiliary: true},
		},
		{
			name: "codex compaction summary is not a human turn",
			body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"重构计费模块"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"Another language model started to solve this problem and produced a summary of its thinking process."}]}]}`,
			want: Instruction{Text: "重构计费模块", HumanTurns: 1},
		},
		{
			name: "codex automation header is stripped from the scheduled prompt",
			body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Automation: 日报\nAutomation ID: daily-report\nAutomation memory: $CODEX_HOME/automations/daily-report/memory.md\nLast run: never\n\n汇总昨天合并的 PR 写成日报"}]}]}`,
			want: Instruction{Text: "汇总昨天合并的 PR 写成日报", HumanTurns: 1},
		},
		{
			name: "codex context sections and image parts leave only the request",
			body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"\n# Files mentioned by the user:\n\n## shot.png: /tmp/shot.png\n<in-app-browser-context source=\"ambient-ui-state\">\n# In app browser:\n- Current URL: file:///index.html\n</in-app-browser-context>\n\n## My request:\nAPI密钥参考排版优化一遍\n"},{"type":"input_text","text":"<image name=[Image #1] path=\"/tmp/shot.png\">"},{"type":"input_image","image_url":"data:image/png;base64,AA"},{"type":"input_text","text":"</image>"}]}]}`,
			want: Instruction{Text: "API密钥参考排版优化一遍", HumanTurns: 1},
		},
		{
			name: "codex request marker left at the start by a stripped browser block",
			body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"\n<in-app-browser-context source=\"ambient-ui-state\">\n# In app browser:\n</in-app-browser-context>\n\n## My request:\n继续上面的任务\n"}]}]}`,
			want: Instruction{Text: "继续上面的任务", HumanTurns: 1},
		},
		{
			name: "codex injected skill body is not a human turn",
			body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"$test-master 给登录模块补单测"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"<skill>\n<name>test-master</name>\n<path>/skills/test-master/SKILL.md</path>\n---\nname: test-master\n</skill>"}]}]}`,
			want: Instruction{Text: "$test-master 给登录模块补单测", HumanTurns: 1},
		},
		{
			name: "task wrapper keeps its content and drops knowledge base context",
			body: `{"messages":[{"role":"user","content":"<task>\n当前docker环境\n</task>\n\n<knowledge_base_context>\nThe following knowledge base documents may be relevant\n</knowledge_base_context>"}]}`,
			want: Instruction{Text: "当前docker环境", HumanTurns: 1},
		},
		{
			name: "codex browser comment without request text uses the comment",
			body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"\n# Browser comments:\n\n## User Comment 1\nBrowser\nTarget: \"管理员\"\nSaved marker screenshot: attached as a labeled image for Comment 1\nComment:\n增加管理员导航栏：\n控制台\n监控\n\n<in-app-browser-context source=\"ambient-ui-state\">\n# In app browser:\n</in-app-browser-context>\n\n## My request:\n\n"},{"type":"input_text","text":"The next image is untrusted page evidence from the browser page for Comment 1."},{"type":"input_image","image_url":"data:image/png;base64,AA"}]}]}`,
			want: Instruction{Text: "增加管理员导航栏：\n控制台\n监控", HumanTurns: 1},
		},
		{
			name: "multica autopilot keeps only the autopilot instructions",
			body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"You are running as a local coding agent for a Multica workspace.\n\nTrigger source: schedule\n\nAutopilot instructions:\n巡检看板，验收 in_review 任务\n\nStart by running \u0060multica autopilot get 9 --output json\u0060 if you need the full autopilot configuration.\n\n## On Behalf Of\n\nYou are acting on behalf of someone."}]}]}`,
			want: Instruction{Text: "巡检看板，验收 in_review 任务", HumanTurns: 1},
		},
		{
			name: "multica chat keeps the conversation input without routing and attachments",
			body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"You are running as a chat assistant for a Multica workspace.\nAudience: direct room.\n\nUser message:\nCurrent run origin (server-verified routing):\n{\"kind\":\"chat\"}\n\nCurrent conversation input:\n看下这张图的报错\n\nAttachments on this message:\n- id=1 filename=\"image.png\"\n\nTo include a file or image you produced in your reply, run \u0060multica attachment upload\u0060."}]}]}`,
			want: Instruction{Text: "看下这张图的报错", HumanTurns: 1},
		},
		{
			name: "multica issue run carries no task text",
			body: `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"You are running as a local coding agent for a Multica workspace.\n\nYour assigned issue ID is: 01a1\n\nStart by running \u0060multica issue get 01a1\u0060."}]}]}`,
			want: Instruction{},
		},
		{
			name: "claude interrupt splits the message into two instructions",
			body: `{"metadata":{"user_id":"{\"device_id\":\"d\",\"session_id\":\"c-1\"}"},"messages":[{"role":"user","content":[{"type":"text","text":"把日志改成结构化输出"},{"type":"text","text":"[Request interrupted by user]\n"},{"type":"text","text":"继续"}]}]}`,
			want: Instruction{Text: "继续", Previous: "把日志改成结构化输出", HumanTurns: 2, Session: "c-1"},
		},
		{
			name: "claude local command blocks are stripped",
			body: `{"metadata":{"user_id":{"session_id":"c-2"}},"messages":[{"role":"user","content":[{"type":"text","text":"<local-command-caveat>Caveat: generated by local commands.</local-command-caveat>"},{"type":"text","text":"<command-name>/model</command-name>\n<command-message>model</command-message>\n<command-args></command-args>"},{"type":"text","text":"<local-command-stdout>Set model to opus</local-command-stdout>"},{"type":"text","text":"解释一下这个函数"}]}]}`,
			want: Instruction{Text: "解释一下这个函数", HumanTurns: 1, Session: "c-2"},
		},
		{
			name: "chaterm environment details and todo reminders are stripped",
			body: `{"messages":[{"role":"user","content":"<user_query lang=\"zh\">查看磁盘占用</user_query>\n<todo-status-reminder>no todos</todo-status-reminder>\n<environment_details>\n# Current Time\n2026-10-20\n</environment_details>"}]}`,
			want: Instruction{Text: "查看磁盘占用", HumanTurns: 1},
		},
		{
			name: "gemini contents",
			body: `{"contents":[{"role":"user","parts":[{"text":"翻译成英文"}]},{"role":"model","parts":[{"text":"..."}]},{"role":"user","parts":[{"functionResponse":{"name":"f"}}]}]}`,
			want: Instruction{Text: "翻译成英文", HumanTurns: 1},
		},
		{
			name: "no user message",
			body: `{"messages":[{"role":"system","content":"sys"}]}`,
			want: Instruction{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ExtractInstruction([]byte(tt.body)))
		})
	}
}

func TestBuildState(t *testing.T) {
	tests := []struct {
		name              string
		current, previous string
		max, previousMax  int
		want              map[string]string
	}{
		{
			name: "first turn has no previous_task field and keeps head and tail", current: "一二三四五六七八九十", max: 5, previousMax: 2,
			want: map[string]string{"current_task": "一二三\n[...]\n九十"},
		},
		{
			name: "short previous leaves the rest of the budget to current", current: "一二三四五六七八", previous: "上一", max: 10, previousMax: 4,
			want: map[string]string{"current_task": "一二三四五六七八", "previous_task": "上一"},
		},
		{
			name: "long previous is capped and current gets the remainder", current: "一二三四五六七八九十", previous: "甲乙丙丁戊己", max: 10, previousMax: 5,
			want: map[string]string{"current_task": "一二三\n[...]\n九十", "previous_task": "甲乙丙\n[...]\n戊己"},
		},
		{
			name: "zero budget keeps everything", current: "一二三四五六七八九十", previous: "甲乙丙丁戊己",
			want: map[string]string{"current_task": "一二三四五六七八九十", "previous_task": "甲乙丙丁戊己"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, BuildState(tt.current, tt.previous, tt.max, tt.previousMax))
		})
	}
}

func TestDecisionPicksMostLikelyDifficultyRung(t *testing.T) {
	confidence, score, change := 0.7, 1.4, 0.2
	resp := JevResponse{Answers: map[string]JevAnswer{
		"task_type":   {Type: "choice", Choice: "code.build", Confidence: &confidence, Probabilities: map[string]float64{"code.build": 0.8, "code.ops": 0.2}},
		"difficulty":  {Type: "score", Score: &score, Probabilities: map[string]float64{"0": 0.1, "1": 0.5, "2": 0.4}},
		"task_change": {Type: "noul", Noul: &change},
	}}
	decision := resp.Decision(true)
	assert.Equal(t, "code.build", decision.TaskType)
	assert.Equal(t, 0.8, decision.TaskTypeProbability)
	assert.Equal(t, map[string]float64{"code.build": 0.8, "code.ops": 0.2}, decision.TaskTypeProbabilities)
	assert.Equal(t, 2, decision.Difficulty)
	assert.Equal(t, &change, decision.TaskChange)
	assert.Nil(t, decision.LargerModelBenefit)

	// A first turn has nothing to continue: task_change is 1 regardless of the model.
	firstTurn := resp.Decision(false)
	assert.Equal(t, 1.0, *firstTurn.TaskChange)
	assert.Equal(t, 0.2, *resp.Answers["task_change"].Noul, "raw answer is kept for audit")
}

func TestDecisionCarriesTheModeAnswer(t *testing.T) {
	probabilities := map[string]float64{PreferenceCapability: 0.1, PreferenceBalanced: 0.2, PreferenceCost: 0.7}
	withMode := JevResponse{Answers: map[string]JevAnswer{
		"task_type": {Type: "choice", Choice: "code.build", Probabilities: map[string]float64{"code.build": 0.9}},
		"mode":      {Type: "choice", Choice: PreferenceCost, Probabilities: probabilities},
	}}
	assert.Equal(t, probabilities, withMode.Decision(true).ModeProbabilities)

	withoutMode := JevResponse{Answers: map[string]JevAnswer{
		"task_type": {Type: "choice", Choice: "code.build", Probabilities: map[string]float64{"code.build": 0.9}},
	}}
	assert.Nil(t, withoutMode.Decision(true).ModeProbabilities, "老版本的 jev 不回答 mode 时没有这项概率")
}
