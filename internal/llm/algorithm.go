package llm

import "fmt"

type Algorithm int

const (
	// RoundRobin is the Algorithm zero value: it's what an LLM built via New gets if
	// SetAlgorithm is never called, and what ParseAlgorithm returns for an omitted config
	// value — both defaults agree.
	RoundRobin Algorithm = iota
	// Order tries providers in config order, first success wins; write "order" explicitly
	// in config to opt into it instead of the RoundRobin default.
	Order
	Random

	// MaxRoundRobinWeight caps the sum of provider weights under round_robin, since that
	// algorithm materializes a cycle slice sized to the total weight.
	MaxRoundRobinWeight = 1000
)

func (a Algorithm) String() string {
	switch a {
	case Order:
		return "order"
	case RoundRobin:
		return "round_robin"
	case Random:
		return "random"
	default:
		return fmt.Sprintf("Algorithm(%d)", int(a))
	}
}

func ParseAlgorithm(s string) (Algorithm, error) {
	switch s {
	case "":
		return RoundRobin, nil
	case "order":
		return Order, nil
	case "round_robin":
		return RoundRobin, nil
	case "random":
		return Random, nil
	default:
		return 0, fmt.Errorf("llm: unknown algorithm %q: must be %q, %q or %q", s, "order", "round_robin", "random")
	}
}
