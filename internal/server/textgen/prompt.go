package textgen

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
)

// instructionsRest is what both kinds of Prompt draft are told after what the Prompt is.
const instructionsRest = `Keep it short and concrete: plain sentences, short lists where they help, no filler. Write in the language of the person's request (in the language of the current text when the request is empty). If a current text is given, improve it as asked and keep what the person wrote unless they ask otherwise.

Use only the material below. Do not use tools, read or write files, run commands, or ask questions.

Reply with the Prompt only: no introduction, no explanation, no code fence.`

// instructions says what to write for the target.
func instructions(target string) string {
	if target == TargetProject {
		return `You write the Prompt of a project in Loop Now. It is added to the system prompt of every Agent in the project, so it says what they share: the goals, conventions, people, and where things are. Loop Now already tells each Agent about its tools, folders and how to message people; do not repeat that.

` + instructionsRest
	}
	return `You write the Prompt of an Agent in Loop Now. The Prompt is added to the Agent's system prompt. Loop Now already tells the Agent about its tools, folders, project and how to message people; do not repeat that, nor what the project's Prompt already says. Address the Agent ("You ..."): its job, what to do and how, what to avoid, when to ask a person, and how to report.

` + instructionsRest
}

// material checks the request and gathers what the model is given: the Agent's name, the project's name and (for an
// Agent) its Prompt, the current text and the request.
func (s *Service) material(ctx context.Context, in DraftRequest) (string, error) {
	if in.Target != TargetAgent && in.Target != TargetProject {
		return "", apperr.Invalid("target", "Must be agent or project.")
	}
	request, current := strings.TrimSpace(in.Request), strings.TrimSpace(in.CurrentText)
	if request == "" && current == "" {
		return "", apperr.Invalid("request", "Say what the Prompt should do, or put some text in the editor to improve.")
	}
	if utf8.RuneCountInString(in.Request) > MaxRequest {
		return "", apperr.Invalid("request", fmt.Sprintf("At most %d characters.", MaxRequest))
	}
	if in.AgentName != nil && utf8.RuneCountInString(*in.AgentName) > MaxAgentName {
		return "", apperr.Invalid("agentName", fmt.Sprintf("At most %d characters.", MaxAgentName))
	}
	var agentName string
	var projectID *uuid.UUID
	switch in.Target {
	case TargetAgent:
		if in.AgentID != nil {
			if in.ProjectID != nil {
				return "", apperr.Invalid("projectId", "Leave it out with agentId: the Agent's own project is used.")
			}
			a, err := s.Store.GetAgent(ctx, *in.AgentID)
			if err != nil {
				return "", err
			}
			agentName, projectID = a.Name, a.ProjectID
		} else {
			projectID = in.ProjectID
		}
		if in.AgentName != nil && strings.TrimSpace(*in.AgentName) != "" {
			agentName = strings.TrimSpace(*in.AgentName)
		}
	case TargetProject:
		if in.AgentID != nil {
			return "", apperr.Invalid("agentId", "Only for an Agent's Prompt.")
		}
		if in.AgentName != nil {
			return "", apperr.Invalid("agentName", "Only for an Agent's Prompt.")
		}
		if in.ProjectID == nil {
			return "", apperr.Invalid("projectId", "Required for a project's Prompt.")
		}
		projectID = in.ProjectID
	}
	var b strings.Builder
	if in.Target == TargetAgent {
		if agentName != "" {
			fmt.Fprintf(&b, "Agent name: %s\n", agentName)
		} else {
			b.WriteString("Agent name: not given yet\n")
		}
	}
	if projectID == nil {
		b.WriteString("Project: none (a platform Agent, not in a project)\n")
	} else {
		p, err := s.Store.GetProject(ctx, *projectID)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "Project: %s\n", p.Name)
		if in.Target == TargetAgent {
			pp, err := s.Store.GetProjectPrompt(ctx, *projectID)
			if err != nil {
				return "", err
			}
			if text := strings.TrimSpace(pp.Prompt); text != "" {
				fmt.Fprintf(&b, "\nThe project's Prompt (every Agent in the project already has it; do not repeat it):\n<project_prompt>\n%s\n</project_prompt>\n", text)
			}
		}
	}
	if current != "" {
		fmt.Fprintf(&b, "\nThe current text, to improve:\n<current_text>\n%s\n</current_text>\n", current)
	} else {
		b.WriteString("\nThe current text: empty (write a new Prompt)\n")
	}
	if request != "" {
		fmt.Fprintf(&b, "\nThe person's request:\n<request>\n%s\n</request>\n", request)
	} else {
		b.WriteString("\nThe person's request: none given; make the current text better.\n")
	}
	// The English labels above pull some models to English; the rule goes last as well.
	b.WriteString("\nWrite the Prompt in the language of the person's request above (of the current text when there is no request), not in English unless that is their language.")
	source := request
	if source == "" {
		source = current
	}
	if lang := scriptLanguage(source); lang != "" {
		fmt.Fprintf(&b, " That language is %s.", lang)
	}
	b.WriteString("\n")
	return b.String(), nil
}

// scriptLanguage names the language of s when one script makes it plain: kana make it Japanese, Hangul Korean, and Han
// (without kana) Chinese, when their characters outnumber the Latin letters; otherwise "".
func scriptLanguage(s string) string {
	var latin, han, kana, hangul int
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r):
			kana++
		case unicode.Is(unicode.Hangul, r):
			hangul++
		case unicode.Is(unicode.Han, r):
			han++
		case unicode.Is(unicode.Latin, r):
			latin++
		}
	}
	switch {
	case kana > 0 && kana+han > latin:
		return "Japanese"
	case hangul > latin && hangul > han:
		return "Korean"
	case han > latin:
		return "Chinese"
	}
	return ""
}
