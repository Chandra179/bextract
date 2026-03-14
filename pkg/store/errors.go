package store

import "errors"

// ErrJobNotFound is returned when a job_id does not exist in the database.
var ErrJobNotFound = errors.New("store: job not found")

// ErrNoPriorTier is returned when a job exists but the required prior tier result is missing.
var ErrNoPriorTier = errors.New("store: required prior tier result not present")
