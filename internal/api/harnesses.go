package api

import "context"

// ModelChoice is a model of a Harness with its effort levels.
type ModelChoice struct {
	// Model is the name of the model
	Model string `gork:"model"`
	// Efforts are the effort levels of the model, or empty for a Harness with no effort levels
	Efforts []string `gork:"efforts"`
}

// HarnessChoices is a Harness with the models that it gives.
type HarnessChoices struct {
	// Harness is the agent program
	Harness string `gork:"harness"`
	// Models are the models of the Harness. They are empty when the read failed.
	Models []ModelChoice `gork:"models"`
	// Error is the reason of a failed read, or empty
	Error string `gork:"error"`
}

// ListHarnessesRequest is the request of ListHarnesses.
type ListHarnessesRequest struct{}

// ListHarnessesResponse is the response of ListHarnesses.
type ListHarnessesResponse struct {
	Body Envelope[[]HarnessChoices]
}

// ListHarnesses returns each Harness on the PATH of the agents with its models and their effort levels.
func (h *handlers) ListHarnesses(ctx context.Context, _ ListHarnessesRequest) (*ListHarnessesResponse, error) {
	choices := h.engine.HarnessChoices(ctx)
	harnesses := make([]HarnessChoices, 0, len(choices))
	for _, choice := range choices {
		models := make([]ModelChoice, 0, len(choice.Models))
		for _, model := range choice.Models {
			efforts := model.Efforts
			if efforts == nil {
				efforts = []string{}
			}
			models = append(models, ModelChoice{Model: model.Model, Efforts: efforts})
		}
		harnesses = append(harnesses, HarnessChoices{Harness: string(choice.Harness), Models: models, Error: choice.Error})
	}
	return &ListHarnessesResponse{Body: Envelope[[]HarnessChoices]{Data: harnesses}}, nil
}
