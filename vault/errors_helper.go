package vault

import "errors"

// goErrorsAs is the indirection used by source.go's errorsAs wrapper —
// keeps the standard `errors` import isolated in this tiny file so the
// main source.go reads cleanly.
func goErrorsAs(err error, target any) bool {
	return errors.As(err, target)
}
