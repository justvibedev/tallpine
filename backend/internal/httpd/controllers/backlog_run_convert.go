package controllers

import (
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
)

func backlogUsageResponse(u backlogsvc.ItemUsage) BacklogUsageResponse {
	return BacklogUsageResponse{CostNanos: u.CostNanos, CostComplete: u.CostComplete, ElapsedSeconds: u.ElapsedSeconds}
}

func backlogRunResponse(v backlogsvc.RunView) BacklogRunResponse {
	items := make([]BacklogRunItemResponse, 0, len(v.Items))
	for _, it := range v.Items {
		items = append(items, BacklogRunItemResponse{
			Ref: it.ItemRef(), ItemID: it.ItemID, Title: it.ItemTitle, Lane: it.Lane, Position: it.Position, Model: it.Model,
			Status: string(it.Status), DisplayStatus: string(it.DisplayStatus), Reason: it.Reason, PlanStatus: string(it.PlanStatus), Question: it.Question,
			Attempts: it.Attempts, UpdatedAt: it.UpdatedAt, Usage: backlogUsageResponse(it.Usage),
		})
	}
	counts := map[string]int{}
	for status, n := range v.Counts {
		counts[string(status)] = n
	}
	refs := v.Selection
	if refs == nil {
		refs = []string{}
	}
	return BacklogRunResponse{
		ID: v.ID, Ref: v.Ref(), ProjectID: string(v.ProjectID), Scope: string(v.Scope), Selection: string(v.SelectionKind),
		Refs: refs, Status: string(v.Status), PauseReason: v.PauseReason, ResumeAt: v.ResumeAt, Summary: v.Summary,
		RequestedBy: string(v.RequestedBy), CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, StartedAt: v.StartedAt, EndedAt: v.EndedAt,
		Items: items, Counts: counts, Usage: backlogUsageResponse(v.Usage),
	}
}
