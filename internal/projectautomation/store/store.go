package store

import "errors"

var ErrNotFound = errors.New("project automation resource not found")
var ErrDuplicate = errors.New("project automation resource already exists")
