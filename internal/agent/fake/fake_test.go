package fake

import (
	"testing"

	"local-symphony/internal/agent"
)

func TestRunnerImplementsAgentRunner(t *testing.T) {
	var _ agent.Runner = Runner{}
}
