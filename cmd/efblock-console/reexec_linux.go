package main

import "syscall"

func execSelf(path string, args, env []string) error { return syscall.Exec(path, args, env) }
