package util

type QuietForgeError struct {
	Message string
	Code    string
}

func (e *QuietForgeError) Error() string {
	return e.Message
}

type ConfigError struct {
	*QuietForgeError
}

func NewConfigError(message string) *ConfigError {
	return &ConfigError{&QuietForgeError{Message: message, Code: "config_error"}}
}

type ProviderError struct {
	*QuietForgeError
	StatusCode int
}

func NewProviderError(message string, statusCode int) *ProviderError {
	return &ProviderError{QuietForgeError: &QuietForgeError{Message: message, Code: "provider_error"}, StatusCode: statusCode}
}

type ToolExecutionError struct {
	*QuietForgeError
	ToolName string
}

func NewToolExecutionError(message string, toolName string) *ToolExecutionError {
	return &ToolExecutionError{QuietForgeError: &QuietForgeError{Message: message, Code: "tool_error"}, ToolName: toolName}
}

type PermissionDeniedError struct {
	*QuietForgeError
}

func NewPermissionDeniedError(message string) *PermissionDeniedError {
	if message == "" {
		message = "Permission denied"
	}
	return &PermissionDeniedError{&QuietForgeError{Message: message, Code: "permission_denied"}}
}

type StorageError struct {
	*QuietForgeError
}

func NewStorageError(message string) *StorageError {
	return &StorageError{&QuietForgeError{Message: message, Code: "storage_error"}}
}

type SessionError struct {
	*QuietForgeError
}

func NewSessionError(message string) *SessionError {
	return &SessionError{&QuietForgeError{Message: message, Code: "session_error"}}
}
