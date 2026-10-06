package llm

import "fmt"

type Algorithm int

const (
	// Order is the zero value: try providers in config order, first success wins. This is
	// the default so a config that omits load_balancing keeps today's exact behavior.
	Order Algorithm = iota
	RoundRobin
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
	case "", "order":
		return Order, nil
	case "round_robin":
		return RoundRobin, nil
	case "random":
		return Random, nil
	default:
		return 0, fmt.Errorf("llm: unknown algorithm %q: must be %q, %q or %q", s, "order", "round_robin", "random")
	}
}
