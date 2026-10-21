// PicoClaw - Ultra-lightweight personal AI agent

package a2a

// turnUsage is the per-turn accounting shown in the owner wake summary.
type turnUsage struct {
	TokensIn      int
	TokensOut     int
	LLMCalls      int
	Iterations    int
	MaxIterations int
}

func (u turnUsage) TotalTokens() int { return u.TokensIn + u.TokensOut }

// inboundResult is the terminal outcome of one inbound turn, collected from
// runtime events instead of a completion callback.
type inboundResult struct {
	Status string
	Reply  string
	Error  string
	Usage  turnUsage
}
