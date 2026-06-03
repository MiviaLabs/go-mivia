package store

import (
	"errors"

	"github.com/MiviaLabs/go-mivia/internal/projectknowledge"
)

var ErrNotFound = errors.New("not found")

type Store = projectknowledge.Store
