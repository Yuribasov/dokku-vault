package plugin

import (
	"fmt"
	"io"
	"strings"
)

func readBounded(reader io.Reader, maximum int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, int64(maximum+1)))
	if err != nil {
		return nil, err
	}
	if len(data) > maximum {
		return nil, fmt.Errorf("input exceeds %d bytes", maximum)
	}
	return data, nil
}

func readSingleLine(reader io.Reader, maximum int, label string) (string, error) {
	data, err := readBounded(reader, maximum)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", label)
	}
	if strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("%s must be one line", label)
	}
	return value, nil
}

func splitFlags(args []string) (map[string]string, map[string]bool, error) {
	values := make(map[string]string)
	booleans := make(map[string]bool)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			return nil, nil, fmt.Errorf("unexpected argument %q", arg)
		}
		name := strings.TrimPrefix(arg, "--")
		if name == "replace" {
			booleans[name] = true
			continue
		}
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			return nil, nil, fmt.Errorf("flag --%s requires a value", name)
		}
		if _, exists := values[name]; exists {
			return nil, nil, fmt.Errorf("flag --%s was supplied more than once", name)
		}
		i++
		values[name] = args[i]
	}
	return values, booleans, nil
}

func requireFlag(flags map[string]string, name string) (string, error) {
	value := strings.TrimSpace(flags[name])
	if value == "" {
		return "", fmt.Errorf("--%s is required", name)
	}
	return value, nil
}

func rejectUnknownFlags(flags map[string]string, allowed ...string) error {
	set := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		set[name] = true
	}
	for name := range flags {
		if !set[name] {
			return fmt.Errorf("unknown flag --%s", name)
		}
	}
	return nil
}
