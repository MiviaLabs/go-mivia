package store

import (
	"strconv"

	model "github.com/MiviaLabs/go-mivia/internal/projectworkplan"
)

func taskPageBounds(total int, filter model.WorkTaskFilter) (int, int, error) {
	offset := 0
	if filter.PageToken != "" {
		parsed, err := strconv.Atoi(filter.PageToken)
		if err != nil || parsed < 0 {
			return 0, 0, model.ErrInvalidInput
		}
		offset = parsed
	}
	if offset >= total {
		return total, total, nil
	}
	limit := filter.PageSize
	if limit <= 0 || offset+limit > total {
		return offset, total, nil
	}
	return offset, offset + limit, nil
}
