package core

import "unicode/utf8"

func utf8Valid(b []byte) bool { return utf8.Valid(b) }
