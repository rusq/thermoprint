//go:build !debug

package cfg

import "flag"

func setDevFlags(fs *flag.FlagSet, mask FlagMask) {}
