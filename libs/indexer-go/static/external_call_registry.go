package static

// externalOp describes what one wrapper function call means at the external service boundary.
type externalOp struct {
	Provider    string // "aws"
	Service     string // "cognito" | "sqs" | "ses"
	Category    string // "identity" | "messaging" | "email"
	DisplayName string // "AWS Cognito"
	Operation   string // underlying SDK op, e.g. "InitiateAuth"
	Variant     string // AuthFlow / "admin" / "" — optional sub-op
	WrapperFunc string // "SignIn" (mirrors the map key, kept for node props)
}

// externalCallRegistry maps (import path → wrapper func name → op).
// Matching is by IMPORT PATH (not the local alias token) for robustness.
var externalCallRegistry = map[string]map[string]externalOp{
	// ---- Phase 1: AWS Cognito ----
	"github.com/tazapay/grpc-framework/client/auth": {
		"SignIn":                          {"aws", "cognito", "identity", "AWS Cognito", "InitiateAuth", "USER_PASSWORD_AUTH", "SignIn"},
		"InitiateCustomAuth":              {"aws", "cognito", "identity", "AWS Cognito", "InitiateAuth", "CUSTOM_AUTH", "InitiateCustomAuth"},
		"GetAccessTokenUsingRefreshToken": {"aws", "cognito", "identity", "AWS Cognito", "InitiateAuth", "REFRESH_TOKEN", "GetAccessTokenUsingRefreshToken"},
		"RespondToAuthChallenge":          {"aws", "cognito", "identity", "AWS Cognito", "RespondToAuthChallenge", "CUSTOM_CHALLENGE", "RespondToAuthChallenge"},
		"Signup":                          {"aws", "cognito", "identity", "AWS Cognito", "SignUp", "", "Signup"},
		"SendVerificationCode":            {"aws", "cognito", "identity", "AWS Cognito", "GetUserAttributeVerificationCode", "", "SendVerificationCode"},
		"ConfirmVerificationCode":         {"aws", "cognito", "identity", "AWS Cognito", "VerifyUserAttribute", "", "ConfirmVerificationCode"},
		"SignOut":                         {"aws", "cognito", "identity", "AWS Cognito", "GlobalSignOut", "", "SignOut"},
		"UpdateUserAttribute":             {"aws", "cognito", "identity", "AWS Cognito", "AdminUpdateUserAttributes", "admin", "UpdateUserAttribute"},
		"ForgotPassword":                  {"aws", "cognito", "identity", "AWS Cognito", "ForgotPassword", "", "ForgotPassword"},
		"ConfirmForgotPassword":           {"aws", "cognito", "identity", "AWS Cognito", "ConfirmForgotPassword", "", "ConfirmForgotPassword"},
		"DisableUser":                     {"aws", "cognito", "identity", "AWS Cognito", "AdminDisableUser", "admin", "DisableUser"},
		"EnableUser":                      {"aws", "cognito", "identity", "AWS Cognito", "AdminEnableUser", "admin", "EnableUser"},
		"GetUser":                         {"aws", "cognito", "identity", "AWS Cognito", "AdminGetUser", "admin", "GetUser"},
		"GetUsers":                        {"aws", "cognito", "identity", "AWS Cognito", "ListUsers", "", "GetUsers"},
		"ChangePassword":                  {"aws", "cognito", "identity", "AWS Cognito", "ChangePassword", "", "ChangePassword"},
		"AdminUserGlobalSignOut":          {"aws", "cognito", "identity", "AWS Cognito", "AdminUserGlobalSignOut", "admin", "AdminUserGlobalSignOut"},
	},

	// ---- Phase 2: AWS SES (notification service) ----
	"github.com/tazapay/grpc-framework/client/email": {
		"SendRaw": {"aws", "ses", "email", "AWS SES", "SendRawEmail", "", "SendRaw"},
	},

	// ---- Phase 3 (SQS — do NOT add a competing detector; wire at the existing OutboxCall site) ----
}
