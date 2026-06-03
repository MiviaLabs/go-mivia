package store

import (
	"errors"

	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
)

var ErrNotFound = errors.New("not found")

type Store = projectevidence.Store
