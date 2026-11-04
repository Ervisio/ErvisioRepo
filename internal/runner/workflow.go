package runner

// Ported from gitea.com/gitea/runner (internal/app/run/workflow.go), MIT
// License, Copyright 2023 The Gitea Authors.

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gitea.com/gitea/runner/act/model"
	"go.yaml.in/yaml/v4"

	"github.com/Ervisio/ErvisioRepo/internal/runnerapi/runnerv1"
)

// generateWorkflow parses the single-job payload of task and adds the
// needed jobs as finished placeholders, so act resolves needs.* from the
// outputs and results sent by the server.
func generateWorkflow(task *runnerv1.Task) (*model.Workflow, string, error) {
	wf, err := model.ReadWorkflow(bytes.NewReader(task.GetWorkflowPayload()))
	if err != nil {
		return nil, "", fmt.Errorf("parse workflow payload: %w", err)
	}
	ids := wf.GetJobIDs()
	if len(ids) != 1 {
		return nil, "", fmt.Errorf("workflow payload must hold exactly one job, got %v", ids)
	}
	jobID := ids[0]

	needIDs := make([]string, 0, len(task.GetNeeds()))
	for id, need := range task.GetNeeds() {
		if id == jobID {
			return nil, "", fmt.Errorf("job %q needs itself", id)
		}
		needIDs = append(needIDs, id)
		wf.Jobs[id] = &model.Job{
			Outputs: need.GetOutputs(),
			Result:  strings.ToLower(strings.TrimPrefix(need.GetResult().String(), "RESULT_")),
		}
	}
	sort.Strings(needIDs)

	raw := yaml.Node{Kind: yaml.SequenceNode, Content: make([]*yaml.Node, 0, len(needIDs))}
	for _, id := range needIDs {
		raw.Content = append(raw.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: id})
	}
	wf.Jobs[jobID].RawNeeds = raw
	return wf, jobID, nil
}
