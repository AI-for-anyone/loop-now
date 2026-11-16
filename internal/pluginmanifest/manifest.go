// Package pluginmanifest reads and validates plugin manifests (plugins/SPEC.md §2). It is shared by the server and
// loopctl, and does not depend on any other server package.
package pluginmanifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ManifestFile is the only manifest file in a plugin directory.
const ManifestFile = "package.json"

// Manifest is a validated manifest, stored as-is in plugins.manifest; event triggers use its events to find
// subscribers. The first part is the API's PluginManifest; the second part holds paths for Pi and the plugin host,
// all relative to the install directory.
type Manifest struct {
	ID                 string        `json:"id"`
	Name               string        `json:"name"`
	Version            string        `json:"version"`
	DisplayName        Text          `json:"displayName"`
	Description        Text          `json:"description"`
	MinPlatformVersion *string       `json:"minPlatformVersion"`
	Events             []string      `json:"events"`
	Permissions        []string      `json:"permissions"`
	FailClosed         bool          `json:"failClosed"`
	ConfigSchema       Schema        `json:"configSchema"`
	Accounts           []AccountKind `json:"accounts"`
	Components         Components    `json:"components"`

	Extensions []string                   `json:"extensions"`
	Skills     []string                   `json:"skills"`
	Platform   string                     `json:"platform"`
	Bin        []string                   `json:"bin"`
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
	// ProviderState is loopNow.providerState: which state of the model providers registered by the plugin travels
	// with the project's accounts (plugins/SPEC.md §13).
	ProviderState map[string]ProviderState `json:"providerState,omitempty"`
	// OwnDisplayName reports that DisplayName is the package's own loopNow.displayName (not a fallback to the npm
	// name or the id). It is only set while reading the manifest and is not stored.
	OwnDisplayName bool `json:"-"`
}

// AccountKind is an account kind declared by the manifest.
type AccountKind struct {
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Fields   Schema `json:"fields"`
	Multiple bool   `json:"multiple"`
}

// Action is a console action button. Fields is the form's JSON Schema, an empty object when there is no input.
type Action struct {
	ID     string   `json:"id"`
	Label  string   `json:"label"`
	Where  []string `json:"where"`
	Fields Schema   `json:"fields"`
}

// Channel is a channel.
type Channel struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// ManageIdentities reports that this channel can manage external identities on their behalf
	// (plugins/SPEC.md §12); it requires the identities.manage scope.
	ManageIdentities bool `json:"manageIdentities"`
}

// ManagingChannels returns the channels declared as able to manage external identities.
func (m Manifest) ManagingChannels() []string {
	out := []string{}
	for _, c := range m.Components.Channels {
		if c.ManageIdentities {
			out = append(out, c.ID)
		}
	}
	return out
}

// Schedule is a scheduled job.
type Schedule struct {
	ID   string `json:"id"`
	Cron string `json:"cron"`
}

// Components is the breakdown of parts shown before installing.
type Components struct {
	AgentExtensions int        `json:"agentExtensions"`
	Skills          []string   `json:"skills"`
	MCPServers      []string   `json:"mcpServers"`
	Bin             []string   `json:"bin"`
	Platform        bool       `json:"platform"`
	Actions         []Action   `json:"actions"`
	Channels        []Channel  `json:"channels"`
	HTTP            bool       `json:"http"`
	Schedules       []Schedule `json:"schedules"`
}

// PublicManifest is the API's PluginManifest (without paths).
type PublicManifest struct {
	ID                 string        `json:"id"`
	Name               string        `json:"name"`
	Version            string        `json:"version"`
	DisplayName        Text          `json:"displayName"`
	Description        Text          `json:"description"`
	MinPlatformVersion *string       `json:"minPlatformVersion"`
	Events             []string      `json:"events"`
	Permissions        []string      `json:"permissions"`
	FailClosed         bool          `json:"failClosed"`
	ConfigSchema       Schema        `json:"configSchema"`
	Accounts           []AccountKind `json:"accounts"`
	Components         Components    `json:"components"`
}

// Public returns the manifest as exposed by the API.
func (m Manifest) Public() PublicManifest {
	return PublicManifest{ID: m.ID, Name: m.Name, Version: m.Version, DisplayName: m.DisplayName, Description: m.Description,
		MinPlatformVersion: m.MinPlatformVersion, Events: m.Events, Permissions: m.Permissions, FailClosed: m.FailClosed,
		ConfigSchema: m.ConfigSchema, Accounts: m.Accounts, Components: m.Components}
}

// HookKind is the kind of an event (plugins/SPEC.md §4).
type HookKind string

const (
	HookIntercept HookKind = "intercept"
	HookModify    HookKind = "modify"
	HookClaim     HookKind = "claim"
	HookObserve   HookKind = "observe"
)

// Events lists every event the platform produces, with its kind.
var Events = map[string]HookKind{
	"run.before_start": HookIntercept, "task.before_start": HookIntercept, "node.before_start": HookIntercept,
	"node.before_submit": HookIntercept, "inbox.before_send": HookIntercept,
	"message.before_enqueue": HookModify, "inbox.before_deliver": HookModify, "sleep.before_schedule": HookModify,
	"console.command": HookClaim,
	"run.queued":      HookObserve, "run.started": HookObserve, "run.ended": HookObserve,
	"task.started": HookObserve, "task.waiting": HookObserve, "task.blocked": HookObserve, "task.completed": HookObserve, "task.cancelled": HookObserve,
	"node.started": HookObserve, "node.submitted": HookObserve, "node.blocked": HookObserve,
	"inbox.received": HookObserve, "inbox.read": HookObserve, "inbox.cancelled": HookObserve,
	"question.asked": HookObserve, "question.answered": HookObserve, "sleep.requested": HookObserve, "sleep.woken": HookObserve,
	"agent.created": HookObserve, "agent.updated": HookObserve, "agent.deleted": HookObserve,
	"pi.started": HookObserve, "pi.exited": HookObserve, "model.retry": HookObserve, "context.compacted": HookObserve,
	"folder.changed": HookObserve, "plugin.enabled": HookObserve, "plugin.disabled": HookObserve, "plugin.config_changed": HookObserve,
	// channel.outbound goes only to the plugin that owns the message thread (plugins/SPEC.md §11); it is not
	// broadcast to subscribers.
	"channel.outbound": HookObserve, "identity.inbound": HookObserve,
}

// Permissions lists the allowed values of the manifest's permissions (plugins/SPEC.md §5).
var Permissions = []string{"agents.read", "agents.write", "tasks.read", "tasks.write", "runs.read", "inbox.read", "inbox.send",
	"folders.read", "folders.write", "identities.read", "identities.manage", "state", "accounts.read"}

var (
	idPattern      = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)
	versionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,63}$`)
	kindPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	slugPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	idFromName     = regexp.MustCompile(`[^a-z0-9]+`)
)

// ValidID reports whether id is a valid plugin id.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// packageJSON is the part of package.json that the platform reads.
type packageJSON struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description Text   `json:"description"`
	Pi          *struct {
		Extensions []string `json:"extensions"`
		Skills     []string `json:"skills"`
	} `json:"pi"`
	LoopNow *struct {
		ID                 string                     `json:"id"`
		DisplayName        Text                       `json:"displayName"`
		MinPlatformVersion *string                    `json:"minPlatformVersion"`
		Platform           string                     `json:"platform"`
		Events             []string                   `json:"events"`
		Permissions        []string                   `json:"permissions"`
		FailClosed         bool                       `json:"failClosed"`
		ConfigSchema       *Schema                    `json:"configSchema"`
		Accounts           []AccountKind              `json:"accounts"`
		MCPServers         map[string]json.RawMessage `json:"mcpServers"`
		Bin                []string                   `json:"bin"`
		Actions            []Action                   `json:"actions"`
		HTTP               bool                       `json:"http"`
		Schedule           []Schedule                 `json:"schedule"`
		Channels           []Channel                  `json:"channels"`
		ProviderState      map[string]ProviderState   `json:"providerState"`
	} `json:"loopNow"`
}

// ReadManifest reads and validates the manifest of a plugin directory: the pi section of package.json (the standard
// Pi package manifest) and the loopNow section. Only this function decides where the manifest lives.
// platformVersion is the platform's release version; when it is not of the form YYYY.MM (a development build),
// minPlatformVersion is not checked.
func ReadManifest(dir, platformVersion string) (Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if errors.Is(err, os.ErrNotExist) {
		return Manifest{}, invalid("插件目录没有 " + ManifestFile)
	}
	if err != nil {
		return Manifest{}, err
	}
	var pkg packageJSON
	if err := json.Unmarshal(b, &pkg); err != nil {
		return Manifest{}, invalid(ManifestFile + " 不是合法的 JSON：" + err.Error())
	}
	if pkg.Pi == nil && pkg.LoopNow == nil {
		return Manifest{}, invalid(ManifestFile + " 既没有 pi 段也没有 loopNow 段，不是插件")
	}
	m := Manifest{Name: pkg.Name, Version: pkg.Version, Description: pkg.Description, Events: []string{}, Permissions: []string{},
		Accounts: []AccountKind{}, Extensions: []string{}, Skills: []string{}, Bin: []string{}, MCPServers: map[string]json.RawMessage{}}
	if !versionPattern.MatchString(m.Version) {
		return Manifest{}, invalid("version 必须是 1 到 64 个字母、数字、点、加号或连字符")
	}
	if pkg.Pi != nil {
		m.Extensions = nonNil(pkg.Pi.Extensions)
		m.Skills = nonNil(pkg.Pi.Skills)
	}
	ln := pkg.LoopNow
	if ln != nil {
		m.ID, m.DisplayName, m.MinPlatformVersion, m.Platform, m.FailClosed = ln.ID, ln.DisplayName, ln.MinPlatformVersion, ln.Platform, ln.FailClosed
		m.Events, m.Permissions, m.Bin = nonNil(ln.Events), nonNil(ln.Permissions), nonNil(ln.Bin)
		if ln.ConfigSchema != nil {
			m.ConfigSchema = *ln.ConfigSchema
		}
		if ln.Accounts != nil {
			m.Accounts = ln.Accounts
		}
		if ln.MCPServers != nil {
			m.MCPServers = ln.MCPServers
		}
		m.Components.Actions, m.Components.Channels, m.Components.Schedules, m.Components.HTTP = ln.Actions, ln.Channels, ln.Schedule, ln.HTTP
		if err := CheckProviderState("loopNow.providerState", ln.ProviderState); err != nil {
			return Manifest{}, err
		}
		m.ProviderState = ln.ProviderState
	}
	if m.ID == "" {
		// A standard Pi package with only a pi section: use the npm name without its scope as the id.
		name := m.Name
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		m.ID = strings.Trim(idFromName.ReplaceAllString(strings.ToLower(name), "-"), "-")
		if len(m.ID) > 64 {
			m.ID = strings.Trim(m.ID[:64], "-")
		}
	}
	if !idPattern.MatchString(m.ID) {
		return Manifest{}, invalid("loopNow.id 必须是 1 到 64 个小写字母、数字或连字符，不以连字符开头或结尾")
	}
	if err := m.Description.Check("description"); err != nil {
		return Manifest{}, err
	}
	if err := m.DisplayName.Check("loopNow.displayName"); err != nil {
		return Manifest{}, err
	}
	m.OwnDisplayName = m.DisplayName.String() != ""
	if !m.OwnDisplayName {
		m.DisplayName = PlainText(m.Name)
		if m.Name == "" {
			m.DisplayName = PlainText(m.ID)
		}
	}
	if err := m.validate(dir, platformVersion); err != nil {
		return Manifest{}, err
	}
	m.Components.AgentExtensions = len(m.Extensions)
	m.Components.Skills, m.Components.Bin, m.Components.Platform = m.Skills, m.Bin, m.Platform != ""
	m.Components.MCPServers = []string{}
	for name := range m.MCPServers {
		m.Components.MCPServers = append(m.Components.MCPServers, name)
	}
	slices.Sort(m.Components.MCPServers)
	if m.Components.Actions == nil {
		m.Components.Actions = []Action{}
	}
	if m.Components.Channels == nil {
		m.Components.Channels = []Channel{}
	}
	if m.Components.Schedules == nil {
		m.Components.Schedules = []Schedule{}
	}
	return m, nil
}

func (m *Manifest) validate(dir, platformVersion string) error {
	if m.MinPlatformVersion != nil && !versionAtLeast(platformVersion, *m.MinPlatformVersion) {
		return invalid(fmt.Sprintf("插件要求平台版本不低于 %s，当前为 %s", *m.MinPlatformVersion, platformVersion))
	}
	if len(m.Extensions) == 0 && len(m.Skills) == 0 && m.Platform == "" && len(m.MCPServers) == 0 && len(m.Bin) == 0 {
		return invalid("插件既没有 Agent 侧（扩展、Skill、MCP 服务器、CLI）也没有平台侧")
	}
	seen := map[string]bool{}
	for _, e := range m.Events {
		if _, ok := Events[e]; !ok {
			return invalid("loopNow.events 中的 " + strconv.Quote(e) + " 不是平台的事件")
		}
		if seen[e] {
			return invalid("loopNow.events 中的 " + strconv.Quote(e) + " 重复")
		}
		seen[e] = true
	}
	for _, p := range m.Permissions {
		if !slices.Contains(Permissions, p) {
			return invalid("loopNow.permissions 中的 " + strconv.Quote(p) + " 不是可用的范围")
		}
	}
	if (len(m.Events) > 0 || len(m.Components.Actions) > 0 || len(m.Components.Channels) > 0 || m.Components.HTTP || len(m.Components.Schedules) > 0) && m.Platform == "" {
		return invalid("订阅事件、操作按钮、渠道、HTTP 与定时都由平台侧处理，须声明 loopNow.platform")
	}
	if m.Platform != "" {
		if err := checkPath(dir, m.Platform, "loopNow.platform", false); err != nil {
			return err
		}
	}
	for _, p := range m.Extensions {
		if err := checkPath(dir, p, "pi.extensions", true); err != nil {
			return err
		}
	}
	for _, p := range m.Skills {
		if err := checkPath(dir, p, "pi.skills", true); err != nil {
			return err
		}
	}
	for _, p := range m.Bin {
		if err := checkPath(dir, p, "loopNow.bin", true); err != nil {
			return err
		}
	}
	for name, raw := range m.MCPServers {
		if err := checkMCPServer(name, raw); err != nil {
			return err
		}
	}
	if t, ok := m.ConfigSchema.Map()["type"]; ok && t != "object" {
		return invalid("loopNow.configSchema 的 type 须为 object")
	}
	if _, err := CompileSchema(m.ConfigSchema.Map(), false); err != nil {
		return invalid("loopNow.configSchema 不是合法的 JSON Schema：" + err.Error())
	}
	kinds := map[string]bool{}
	for _, a := range m.Accounts {
		if !kindPattern.MatchString(a.Kind) || kinds[a.Kind] {
			return invalid("loopNow.accounts 的 kind 须唯一，由小写字母、数字或下划线组成，以字母开头")
		}
		kinds[a.Kind] = true
		if a.Fields.IsZero() {
			return invalid("loopNow.accounts 的 " + a.Kind + " 缺少 fields")
		}
		if _, err := CompileSchema(a.Fields.Map(), true); err != nil {
			return invalid("loopNow.accounts 的 " + a.Kind + " fields 不是合法的 JSON Schema：" + err.Error())
		}
	}
	ids := map[string]bool{}
	for _, a := range m.Components.Actions {
		if !slugPattern.MatchString(a.ID) || ids[a.ID] || a.Label == "" || len(a.Where) == 0 {
			return invalid("loopNow.actions 的每项须有唯一的 id、label 与 where")
		}
		if !a.Fields.IsZero() {
			if _, err := CompileSchema(a.Fields.Map(), true); err != nil {
				return invalid("loopNow.actions 的 " + a.ID + " fields 不是合法的 JSON Schema：" + err.Error())
			}
		}
		ids[a.ID] = true
		for _, w := range a.Where {
			if !slices.Contains([]string{"agent", "task", "project", "identity", "global"}, w) {
				return invalid("loopNow.actions 的 where 只能是 agent、task、project、identity 或 global")
			}
		}
	}
	channelIDs := map[string]bool{}
	for _, c := range m.Components.Channels {
		if !slugPattern.MatchString(c.ID) || c.Label == "" || channelIDs[c.ID] {
			return invalid("loopNow.channels 的每项须有唯一的 id 与 label")
		}
		channelIDs[c.ID] = true
		if c.ManageIdentities && !slices.Contains(m.Permissions, "identities.manage") {
			return invalid("loopNow.channels 的 " + c.ID + " 能代管外部身份，须在 permissions 里声明 identities.manage")
		}
	}
	scheduleIDs := map[string]bool{}
	for _, sc := range m.Components.Schedules {
		if !slugPattern.MatchString(sc.ID) || scheduleIDs[sc.ID] {
			return invalid("loopNow.schedule 的每项须有唯一的 id")
		}
		scheduleIDs[sc.ID] = true
		if _, err := ParseCron(sc.Cron); err != nil {
			return invalid("loopNow.schedule 的 " + sc.ID + "：" + err.Error())
		}
	}
	return nil
}

// checkPath requires a manifest path to be inside the plugin directory and to exist; when allowDir is false it
// must be a file.
func checkPath(dir, rel, field string, allowDir bool) error {
	clean := filepath.Clean(rel)
	if rel == "" || filepath.IsAbs(rel) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return invalid(field + " 的 " + strconv.Quote(rel) + " 须是插件目录内的相对路径")
	}
	info, err := os.Stat(filepath.Join(dir, clean))
	if err != nil {
		return invalid(field + " 的 " + strconv.Quote(rel) + " 不存在")
	}
	if info.IsDir() && !allowDir {
		return invalid(field + " 的 " + strconv.Quote(rel) + " 须是文件")
	}
	return nil
}

// checkMCPServer checks one mcpServers entry using the rules and the order of checks of Pi's mcp.json: type may be
// omitted and can only be stdio, http or streamable-http (Pi does not support SSE); an entry with a url whose type
// is not stdio is a streamable HTTP entry, and the url must be an http or https address; otherwise the entry must
// have a non-empty command and a type that is not one of the http kinds. headers, args, env and the other fields are
// passed to Pi as-is. The error texts are the same as the SDK's validate.
func checkMCPServer(name string, raw json.RawMessage) error {
	field := "loopNow.mcpServers 的 " + strconv.Quote(name)
	bad := invalid(field + " 须是带 command 的 stdio 条目或带 http/https url 的 streamable HTTP 条目，名称为小写字母、数字、下划线或连字符")
	var srv map[string]any
	if !slugPattern.MatchString(name) || json.Unmarshal(raw, &srv) != nil || srv == nil {
		return bad
	}
	typ, _ := srv["type"].(string)
	if v, ok := srv["type"]; ok && !slices.Contains([]string{"stdio", "http", "streamable-http"}, typ) {
		if v == "sse" {
			return invalid(field + " 不支持 SSE，请用 streamable HTTP 的 url")
		}
		return invalid(field + " 的 type 只能是 stdio、http 或 streamable-http")
	}
	if u, ok := srv["url"].(string); ok && typ != "stdio" {
		// Same as the SDK: it must be written as http:// or https:// plus a host (Pi's URL.canParse normalizes
		// "http:foo" to http://foo/; we do not accept that here), and the port must not exceed 65535 (URL.canParse
		// rejects it, url.Parse does not check).
		p, err := url.Parse(u)
		if err != nil || p.Scheme != "http" && p.Scheme != "https" || p.Host == "" || p.Hostname() == "" {
			return bad
		}
		if port := p.Port(); port != "" {
			if n, err := strconv.Atoi(port); err != nil || n > 65535 {
				return bad
			}
		}
		return nil
	}
	if c, _ := srv["command"].(string); c != "" && typ != "http" && typ != "streamable-http" {
		return nil
	}
	return bad
}

// versionAtLeast compares release versions of the form YYYY.MM; when the platform version is not of that form (a
// development build) the requirement counts as met.
func versionAtLeast(platform, required string) bool {
	have, ok := releaseParts(platform)
	if !ok {
		return true
	}
	want, ok := releaseParts(required)
	if !ok {
		return false
	}
	for i := range max(len(have), len(want)) {
		var h, w int
		if i < len(have) {
			h = have[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if h != w {
			return h > w
		}
	}
	return true
}

func releaseParts(v string) ([]int, bool) {
	fields := strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '-' || r == '+' })
	var out []int
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out, len(out) >= 2
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// Error describes how a manifest or a config breaks the rules.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func invalid(message string) error {
	return &Error{Message: message}
}
