package store

import "errors"

var ErrNotFound = errors.New("project work plan resource not found")
var ErrDuplicate = errors.New("project work plan resource already exists")
