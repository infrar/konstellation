package files

import "os"

var (
	DefaultDirectoryMode = os.FileMode(0755)
	DefaultFileMode      = os.FileMode(0644)
	ExecutableFileMode   = os.FileMode(0755)
)
