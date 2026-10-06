package llm

// MaxRoundRobinWeight caps the sum of provider weights under round_robin, since that
// algorithm materializes a cycle slice sized to the total weight.
const MaxRoundRobinWeight = 1000

// weighter is implemented by providers that carry their own weight (gemini/huggingFace
// providers, set from config); providers that don't implement it default to weight 1.
type weighter interface{ Weight() int }

func providerWeight(p Provider) int {
	if w, ok := p.(weighter); ok {
		return w.Weight()
	}
	return 1
}
