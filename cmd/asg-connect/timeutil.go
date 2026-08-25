package main

import "time"

func timeNano() int64 { return time.Now().UnixNano() }
