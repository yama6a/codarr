// Not a real module. It exists so `go build ./...` and `go test ./...` at the
// repo root stop at web/, because npm packages occasionally ship Go source
// (flatted does) and Go's ... pattern does not skip node_modules.
//
// There is no Go code under web/. Do not add any.
module github.com/yama6a/codarr/web

go 1.27.0
