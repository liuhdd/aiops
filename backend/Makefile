.PHONY: test eval eval-ci

test:
	go test ./...

eval:
	go run ./cmd/eval run --trials 3 --configs rule,llm-single,llm-react --report eval-report.json

eval-ci:
	go run ./cmd/eval ci --threshold 0.9
