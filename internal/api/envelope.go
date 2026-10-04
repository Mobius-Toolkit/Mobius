package api

// Envelope is the body of each success response.
type Envelope[T any] struct {
	// Data is the payload of the response
	Data T `gork:"data"`
}
