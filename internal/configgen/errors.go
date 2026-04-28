package configgen

import "fmt"

func wrapErr(context string, err error) error {
	return fmt.Errorf("%s: %w", context, err)
}
