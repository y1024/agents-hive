package qualityworkbench

import (
	"context"

	"github.com/chef-guo/agents-hive/internal/agentquality"
)

func runEvalRunner(ctx context.Context, runner agentquality.EvalRunner, cases []agentquality.LoadedCase) (agentquality.GateInput, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if contextRunner, ok := runner.(agentquality.ContextEvalRunner); ok {
		return contextRunner.RunWithContext(ctx, cases)
	}
	return runner.Run(cases)
}
