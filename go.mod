module github.com/tcheremkhina/golang-toy-bazel

go 1.25.0

require (
	github.com/golang/mock v1.6.0
	github.com/spf13/cobra v1.10.1
	github.com/stretchr/testify v1.11.1
	gitlab.com/slon/shad-go v0.0.0-20231003165454-50b27acb6315
	go.uber.org/goleak v1.3.0
	go.uber.org/zap v1.27.0
	golang.org/x/perf v0.0.0-20250909190841-7e13e04d9366
	golang.org/x/sys v0.36.0
	golang.org/x/tools v0.36.0
	gopkg.in/yaml.v2 v2.4.0
)

require (
	github.com/google/safehtml v0.0.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	golang.org/x/mod v0.27.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	golang.org/x/sync v0.17.0
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace gitlab.com/slon/shad-go => github.com/tcheremkhina/golang-toy-bazel v0.0.0-20250926144910-058d2cf1456e
