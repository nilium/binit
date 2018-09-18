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
	"bytes"
	"flag"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	ini "go.spiff.io/go-ini"

	stdlog "log"
)

type Strings []string

func (s *Strings) String() string {
	return "[]"
}

func (s *Strings) Set(str string) error {
	*s = append(*s, str)
	return nil
}

// compileWildcard converts a splat string (a string containing either ? or * to indicate a match-one or match-zero-to-N
// wildcard, respectively) to a regular expression for string matching. This is the rough equivalent of taking
// instructions to dig a hole and starting a mine leading down to the center of the earth, but the alternative was using
// my glob package, and I kind of want to restrict the number of outside packages, even my own, for binit.
func compileWildcard(splat string) (*regexp.Regexp, error) {
	var b bytes.Buffer
	b.Grow(len(splat) + 2)
	escape := false
	b.WriteByte('^')
	for _, r := range splat {
		if r == '\\' {
			escape = true
			continue
		}

		if escape {
			b.WriteString(regexp.QuoteMeta(string(r)))
		} else if r == '*' {
			b.WriteString(".*")
		} else if r == '?' {
			b.WriteString(".")
		} else {
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
		escape = false
	}

	if escape {
		b.WriteString(`\\`)
	}

	b.WriteByte('$')

	pat := b.String()
	return regexp.Compile(pat)
}

func log(args ...interface{}) { stdlog.Print(args...) }

func main() {
	stdlog.SetPrefix("binit: ")
	stdlog.SetFlags(0)

	var assigned []string

	dropRepeats := flag.Bool("n", false, "Whether to pick only the last-set value for an environment value.")
	keepFirst := flag.Bool("N", false, "Keep first values instead of last (implies -n).")
	casingFlag := flag.String("c", "s", "Case transformations to apply to keys. (c=case-sensitive; u=uppercase; d=lowercase)")
	configLast := flag.Bool("L", false, "Gives config file values precedence over values from the environment.")
	ksep := flag.String("S", ".", "The string `separator` inserted between group names and keys.")
	sep := flag.String("s", " ", "The string `separator` inserted between multi-value keys. May include Go escape characters if quoted according to Go.")
	clean := flag.Bool("i", false, "Whether to omit current environment variables from the exec.")
	var imports = new(Strings)
	var inputs = new(Strings)

	flag.Var(imports, "m", "Import a specific variable from the environment. Implies -i.")
	flag.Var((*Strings)(&assigned), "e", "Set an environment variable (`K=V`).")
	flag.Var(inputs, "f", "INI `file`s to load into the environment. (Pass - to read from standard input.)")

	flag.Parse()

	if *keepFirst {
		*dropRepeats = true
	}

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

	// Load process environment
	current := parseEnv(os.Environ())

	// Merge imported environment values

	copyCurrent := !*clean && len(*imports) == 0
	importValues := func() {
		copyValues(values, parseEnv(assigned))
		if copyCurrent {
			copyValues(values, current)
		} else {
			copyImports(values, current, *imports)
		}
	}

	if !*configLast { // Append environment before loading config files
		importValues()
	}

	dec := ini.Reader{
		Separator: *ksep,
		Casing:    parseCasing(*casingFlag),
		True:      ini.True,
	}
	for _, path := range *inputs {
		importConfigFile(values, path, &dec)
	}

	if *configLast { // Append environment after loading config files
		importValues()
	}

	env := compileEnv(values, *dropRepeats, *keepFirst, *sep)
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

func compileEnv(src map[string][]string, dropRepeats, keepFirst bool, sep string) []string {
	env := make([]string, 0, len(src))
	for k, v := range src {
		var pair string
		if dropRepeats {
			keptIndex := 0
			if !keepFirst {
				keptIndex = len(v) - 1
			}
			pair = k + "=" + v[keptIndex]
		} else {
			pair = k + "=" + strings.Join(v, sep)
		}
		env = append(env, pair)
	}
	return env
}

func copyImports(dst map[string][]string, src map[string]string, imports Strings) {
	for _, m := range imports {
		if !strings.ContainsAny(m, "*?") {
			copyLiteral(dst, src, m)
			continue
		}

		pat, err := compileWildcard(m)
		if err != nil {
			log("unable to compile pattern-like import", strconv.Quote(m), ": ", err)
			copyLiteral(dst, src, m)
			continue
		}

		for k, v := range src {
			if _, ok := dst[k]; ok || !pat.MatchString(k) {
				continue
			}
			dst[k] = []string{v}
		}
	}
}

func copyLiteral(dst map[string][]string, src map[string]string, name string) {
	if v, ok := src[name]; ok {
		dst[name] = append(dst[name], v)
	}
}

func copyValues(dst map[string][]string, src map[string]string) {
	for k, v := range src {
		dst[k] = append(dst[k], v)
	}
}

func parseEnv(environ []string) map[string]string {
	env := map[string]string{}
	for _, pair := range environ {
		idx := strings.IndexByte(pair, '=')
		if idx == -1 {
			env[pair] = ""
		} else {
			env[pair[:idx]] = pair[idx+1:]
		}
	}
	return env
}

func parseCasing(opt string) ini.KeyCase {
	switch strings.ToLower(opt) {
	case "", "s", "cs", "cased", "case-sensitive":
	case "u", "up", "upper":
		return ini.UpperCase
	case "l", "d", "down", "lower":
		return ini.LowerCase
	default:
		log("invalid case flag: ", strconv.Quote(opt), "; using default of \"case-sensitive\"")
	}
	return ini.CaseSensitive
}

func importConfigFile(dst map[string][]string, path string, dec *ini.Reader) {
	var err error
	var b []byte

	if path == "-" {
		b, err = ioutil.ReadAll(os.Stdin)
	} else {
		b, err = ioutil.ReadFile(path)
	}

	if err != nil {
		log("error reading <", path, ">:", err)
		return
	}

	err = dec.Read(bytes.NewReader(b), ini.Values(dst))
	if err != nil {
		log("error parsing INI ", path, ": ", err)
	}
}
