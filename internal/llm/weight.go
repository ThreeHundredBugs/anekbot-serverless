package llm

// MaxRoundRobinWeight caps the sum of provider weights under round_robin, since that
// algorithm materializes a cycle slice sized to the total weight.
const MaxRoundRobinWeight = 1000

// WithWeight wraps p so round_robin and random pick it with the given weight instead of the
// default 1. order ignores weight entirely.
func WithWeight(p Provider, weight int) Provider {
	return weightedProvider{Provider: p, n: weight}
}

type weightedProvider struct {
	Provider
	n int
}

func (w weightedProvider) weight() int { return w.n }

type weighter interface{ weight() int }

func providerWeight(p Provider) int {
	if w, ok := p.(weighter); ok {
		return w.weight()
	}
	return 1
}
