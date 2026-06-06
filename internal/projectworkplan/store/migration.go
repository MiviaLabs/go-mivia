package store

import (
	"context"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
)

var workPlanMetadataLabels = []string{
	labelWorkPlan,
	labelWorkTask,
	labelWorkTaskEvidenceAttachment,
	labelWorkTaskContextPackAttachment,
	labelWorkTaskClaimAttachment,
	labelWorkTaskVerifierResultAttachment,
	labelWorkTaskReviewResultAttachment,
	labelWorkTaskKnowledgeAttachment,
}

func MigrateLadybugMetadata(ctx context.Context, source ladybug.Graph, target ladybug.Graph, projectIDs []string) error {
	if source == nil || target == nil {
		return nil
	}
	write := func(graph ladybug.Graph) error {
		for _, projectID := range projectIDs {
			if projectID == "" {
				continue
			}
			for _, label := range workPlanMetadataLabels {
				nodes, err := source.ListNodes(ctx, label, map[string]string{"project_id": projectID})
				if err != nil {
					return err
				}
				for _, node := range nodes {
					if err := graph.PutNode(ctx, node); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if batch, ok := target.(ladybug.BatchGraph); ok {
		return batch.Batch(ctx, write)
	}
	return write(target)
}
