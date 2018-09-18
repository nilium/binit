PKG := go.spiff.io/binit
PROG ?= $(notdir $(PKG))

SOURCES := $(shell go list -f '{{.ImportPath}}{{"\n"}}{{range .Deps}}{{.}}{{"\n"}}{{end}}' "$(PKG)" | xargs go list -f '{{$$dir := .Dir}}{{range .GoFiles}}{{$$dir}}/{{.}}{{"\n"}}{{end}}')

.PHONY: all clean

all: $(PROG) $(PROG).1

$(PROG): $(SOURCES)
	go build -o "$@"

$(PROG).1: README.adoc
	asciidoctor --out-file=- -b manpage "$<" > "$@"

clean:
	go clean
	$(RM) "$(PROG)" "$(PROG).1"
