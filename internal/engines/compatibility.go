package engines

import "sort"

// Compatibility describes a known engine that is intentionally not part of
// the runnable registry for this build.
type Compatibility struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Reference string `json:"reference"`
}

var compatibilityCatalog = map[string]Compatibility{
	"dalle-mini": {
		ID:        "dalle-mini",
		Status:    "unsupported",
		Code:      "DALL_E_MINI_UNSUPPORTED",
		Message:   "DALL-E Mini requires a separately pinned JAX/Flax worker environment and is not supported by this PyTorch/XPU build",
		Reference: "https://github.com/miloszkolber/uncanny-lab#dall-e-mini-compatibility",
	},
}

func CompatibilityFor(id string) (Compatibility, bool) {
	value, ok := compatibilityCatalog[id]
	return value, ok
}

func CompatibilityList() []Compatibility {
	result := make([]Compatibility, 0, len(compatibilityCatalog))
	for _, value := range compatibilityCatalog {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
