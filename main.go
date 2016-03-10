// Command binit is an env-like tool to exec programs. In addition to being able to use or discard the current
// environment and pass environment variables on the command line, they may also be loaded from .ini files (as passed in
// via the -f option).
//
// For example:
//
//   $ binit -e thing.var=value -f config.ini -i sh -c export
//   export section.key="value"
//   export section.with-newlines="value
//   with
//   newlines"
//   export thing.var="value"
//
package main

import (
	"flag"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"

	stdlog "log"

	"go.spiff.io/go-ini"
)

type Strings []string

func (s *Strings) String() string {
	return "[]"
}

func (s *Strings) Set(str string) error {
	*s = append(*s, str)
	return nil
}

func log(args ...interface{}) { stdlog.Print(args...) }

func main() {
	stdlog.SetPrefix("binit: ")
	stdlog.SetFlags(0)

	var env []string

	clean := flag.Bool("i", false, "Whether to omit current environment variables from the exec.")
	var inputs = new(Strings)

	flag.Var((*Strings)(&env), "e", "Set an environment variable (`K=V`).")
	flag.Var(inputs, "f", "INI `file`s to load into the environment. (Pass - to read from standard input.)")

	flag.Parse()

	var values = map[string]string{}
	if !*clean {
		env = append(os.Environ(), env...)
	}

	for _, e := range env {
		off := strings.IndexByte(e, '=')
		if off == -1 {
			values[e] = ""
		} else {
			values[e[:off]] = e[off+1:]
		}
	}

	env = env[:0]

	var err error
	for _, fp := range *inputs {
		var b []byte

		if fp == "-" {
			b, err = ioutil.ReadAll(os.Stdin)
		} else {
			b, err = ioutil.ReadFile(fp)
		}

		if err != nil {
			log("error reading <", fp, ">:", err)
			continue
		}

		values, err = ini.ReadINI(b, values)
		if err != nil {
			log("error parsing INI ", fp, ": ", err)
		}
	}

	for k, v := range values {
		pair := k + "=" + v
		env = append(env, pair)
	}

	sort.Strings(env)

	argv := flag.Args()
	if len(argv) == 0 {
		for _, pair := range env {
			io.WriteString(os.Stdout, pair+"\n")
		}
		return
	}

	cmd, err := exec.LookPath(argv[0])
	if err != nil {
		log(err)
		os.Exit(127)
	}

	argv[0] = cmd

	if err := syscall.Exec(cmd, argv, env); err != nil {
		log("error exec-ing to <", cmd, ">: ", err)
		os.Exit(126)
	}

	log("exec failed, process still running")
	os.Exit(1)
}
