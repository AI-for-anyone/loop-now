package tasks

import (
	"context"
	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/taskmodel"
)

func (s *Service) GetLarkSwitchParticipants(ctx context.Context, p auth.Principal, id uuid.UUID) (taskmodel.Detail, []taskmodel.LarkSwitchParticipant, error) {
	if p.Kind != auth.KindConsole {
		return taskmodel.Detail{}, nil, apperr.Forbidden("任务凭证参与者仅限控制台")
	}
	d, participants, err := s.Store.TaskLarkParticipants(ctx, id)
	if err != nil {
		return d, nil, err
	}
	d, err = s.QuestionViews(ctx, p, d)
	return d, participants, err
}
