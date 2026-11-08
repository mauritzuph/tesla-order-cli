//go:build !darwin

package urlhandler

func Supported() bool                { return false }
func Install(string) (string, error) { return "", ErrUnsupported }
func Uninstall(string) (bool, error) { return false, ErrUnsupported }
func Installed(string) bool          { return false }
func RemoveLegacy(string) bool       { return false }
func CurrentHandler() string         { return "" }
