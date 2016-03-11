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
	"strconv"
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

	dropRepeats := flag.Bool("n", false, "Whether to pick only the last-set value for an environment value.")
	sep := flag.String("s", " ", "The string `separator` inserted between multi-value keys. May include Go escape characters if quoted according to Go.")
	clean := flag.Bool("i", false, "Whether to omit current environment variables from the exec.")
	var imports = new(Strings)
	var inputs = new(Strings)

	flag.Var(imports, "m", "Import a specific variable from the environment. Implies -i.")
	flag.Var((*Strings)(&env), "e", "Set an environment variable (`K=V`).")
	flag.Var(inputs, "f", "INI `file`s to load into the environment. (Pass - to read from standard input.)")

	flag.Parse()

	if s := *sep; len(s) > 0 {
		var err error
		// It's only going to be a valid Go quote if it starts with a character in ASCII range, so no need to worry about decoding a rune here.
		switch s[0] {
		case '`', '\'', '"':
			s, err = strconv.Unquote(s)
		default:
			s, err = strconv.Unquote(`"` + strings.Replace(s, `"`, `\"`, -1) + `"`)
		}
		if err == nil {
			*sep = s
		} else {
			log("unable to unquote separator: ", strconv.Quote(*sep))
		}
	}

	var values = map[string][]string{}

	if len(*imports) > 0 {
		*clean = true
	}

	for _, m := range *imports {
		if _, ok := values[m]; ok {
			continue
		} else if v := os.Getenv(m); v != "" {
			values[m] = []string{v}
		}
	}

	if !*clean {
		env = append(os.Environ(), env...)
	}

	for _, e := range env {
		off := strings.IndexByte(e, '=')
		if off == -1 {
			values[e] = append(values[e], "")
		} else {
			k, v := e[:off], e[off+1:]
			values[k] = append(values[k], v)
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
		var pair string
		if *dropRepeats {
			pair = k + "=" + v[len(v)-1]
		} else {
			pair = k + "=" + strings.Join(v, *sep)
		}
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
