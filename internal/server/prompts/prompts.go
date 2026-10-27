// Package prompts composes explicit captured text; it never reads configuration.
package prompts

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
)

const FormatV1 = "project-agent-v1"

type ProjectRules struct {
	ID      uuid.UUID
	Prompt  string
	Version int64
}
type Layers struct {
	Format         string     `json:"format"`
	ProjectID      *uuid.UUID `json:"projectId"`
	ProjectPrompt  string     `json:"projectPrompt"`
	ProjectVersion int64      `json:"projectVersion"`
	AgentPrompt    string     `json:"agentPrompt"`
	AgentVersion   int64      `json:"agentVersion"`
}

func (l Layers) Validate() error {
	if l.Format != FormatV1 || l.AgentVersion < 1 {
		return errors.New("Prompt组合版本无效")
	}
	if l.ProjectID == nil {
		if l.ProjectPrompt != "" || l.ProjectVersion != 0 {
			return errors.New("平台Agent不能继承项目Prompt")
		}
	} else if *l.ProjectID == uuid.Nil || l.ProjectVersion < 1 {
		return errors.New("项目Prompt版本无效")
	}
	return nil
}
func (l Layers) Compose() (string, error) {
	if err := l.Validate(); err != nil {
		return "", err
	}
	if l.ProjectID == nil || l.ProjectPrompt == "" {
		return l.AgentPrompt, nil
	}
	out := "项目规则优先；Agent规则在项目规则范围内补充。\n\n## 项目公共规则\n" + l.ProjectPrompt
	if l.AgentPrompt != "" {
		out += "\n\n## Agent职责与规则\n" + l.AgentPrompt
	}
	return out, nil
}
func Clone(l *Layers) *Layers {
	if l == nil {
		return nil
	}
	out := *l
	if l.ProjectID != nil {
		id := *l.ProjectID
		out.ProjectID = &id
	}
	return &out
}
func Identity(l *Layers) string {
	if l == nil {
		return "legacy"
	}
	project := "platform"
	if l.ProjectID != nil {
		project = l.ProjectID.String()
	}
	return fmt.Sprintf("%s/%s/%d/%d", l.Format, project, l.ProjectVersion, l.AgentVersion)
}
