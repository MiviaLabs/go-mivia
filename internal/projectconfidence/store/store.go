package store

import (
	"errors"

	"github.com/MiviaLabs/go-mivia/internal/projectconfidence"
)

var ErrNotFound = errors.New("not found")

type Store = projectconfidence.Store
