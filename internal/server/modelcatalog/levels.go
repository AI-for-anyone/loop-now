package modelcatalog

import (
	"slices"
	"strings"
)

// idLevels are the thinking levels a CLI's model id can end with when it lists a model once per level (all the
// platform's levels but off), in the platform's order.
var idLevels = thinkingLevels[1:]

// MergeLevelModels folds the rows of a CLI's model list that name one model at several thinking levels into one model
// (agy lists gemini-4-neon-low "Gemini 4 Neon (Low)", gemini-4-neon-high "Gemini 4 Neon (High)" and so on). A row is
// one level of a model when its id ends with -<level> and its name with " (<Level>)", the same level of idLevels (the
// name in any case; the id in lowercase, as LevelModelID sends it back). The model takes the place of its first row,
// with that row's other fields, the id and name without the level, LevelIDs set, and the levels it was listed at as
// its Efforts, in the platform's order. Every other row stays as it was, and so do all the rows of a model whose plain
// id is listed too.
func MergeLevelModels(rows []LocalModel) []LocalModel {
	listed := map[string]bool{}
	for _, m := range rows {
		listed[m.ID] = true
	}
	out := make([]LocalModel, 0, len(rows))
	merged := map[string]int{} // model id to its index in out
	for _, m := range rows {
		id, name, level, ok := rowLevel(m)
		if !ok || listed[id] {
			out = append(out, m)
			continue
		}
		i, seen := merged[id]
		if !seen {
			model := m
			model.ID, model.Name, model.Efforts, model.DefaultEffort, model.LevelIDs = id, name, nil, "", true
			i = len(out)
			merged[id] = i
			out = append(out, model)
		}
		if !slices.Contains(out[i].Efforts, level) {
			out[i].Efforts = append(out[i].Efforts, level)
		}
	}
	for _, i := range merged {
		slices.SortFunc(out[i].Efforts, func(a, b string) int { return slices.Index(idLevels, a) - slices.Index(idLevels, b) })
	}
	return out
}

// rowLevel splits a row that names a model at one thinking level into the model's id and name and the level.
func rowLevel(m LocalModel) (id, name, level string, ok bool) {
	for _, level := range idLevels {
		id, found := strings.CutSuffix(m.ID, "-"+level)
		suffix := " (" + level + ")"
		if !found || id == "" || len(m.Name) <= len(suffix) || !strings.EqualFold(m.Name[len(m.Name)-len(suffix):], suffix) {
			continue
		}
		if name := strings.TrimSpace(m.Name[:len(m.Name)-len(suffix)]); name != "" {
			return id, name, level, true
		}
	}
	return "", "", "", false
}

// LevelModelID is the id under which a CLI that lists a model once per thinking level (see MergeLevelModels) runs the
// model at level: <id>-<level>. Off, no level, or a level no id ends with leaves id as it is (a model listed once, or
// an id recorded before the levels were merged, which already names its level).
func LevelModelID(id, level string) string {
	if slices.Contains(idLevels, level) {
		return id + "-" + level
	}
	return id
}

// ValidThinkingLevel reports whether level is one of the thinking levels the platform knows.
func ValidThinkingLevel(level string) bool { return slices.Contains(thinkingLevels, level) }
