package static

// externalOp describes what one wrapper function call means at the external service boundary.
type externalOp struct {
	Provider       string // "aws"
	Service        string // "cognito" | "sqs" | "ses" | "s3"
	Category       string // "identity" | "messaging" | "email" | "storage"
	DisplayName    string // "AWS Cognito"
	Operation      string // underlying SDK op, e.g. "InitiateAuth"
	Variant        string // AuthFlow / "admin" / "" — optional sub-op
	WrapperFunc    string // "SignIn" (mirrors the map key, kept for node props)
	BucketArgIndex int    // 0-indexed position in call.Args holding the bucket string; -1 = no extraction (struct arg or N/A)
}

// externalCallRegistry maps (import path → wrapper func name → op).
// Matching is by IMPORT PATH (not the local alias token) for robustness.
var externalCallRegistry = map[string]map[string]externalOp{
	// ---- Phase 1: AWS Cognito ----
	"github.com/tazapay/grpc-framework/client/auth": {
		"SignIn":                          {"aws", "cognito", "identity", "AWS Cognito", "InitiateAuth", "USER_PASSWORD_AUTH", "SignIn", -1},
		"InitiateCustomAuth":              {"aws", "cognito", "identity", "AWS Cognito", "InitiateAuth", "CUSTOM_AUTH", "InitiateCustomAuth", -1},
		"GetAccessTokenUsingRefreshToken": {"aws", "cognito", "identity", "AWS Cognito", "InitiateAuth", "REFRESH_TOKEN", "GetAccessTokenUsingRefreshToken", -1},
		"RespondToAuthChallenge":          {"aws", "cognito", "identity", "AWS Cognito", "RespondToAuthChallenge", "CUSTOM_CHALLENGE", "RespondToAuthChallenge", -1},
		"Signup":                          {"aws", "cognito", "identity", "AWS Cognito", "SignUp", "", "Signup", -1},
		"SendVerificationCode":            {"aws", "cognito", "identity", "AWS Cognito", "GetUserAttributeVerificationCode", "", "SendVerificationCode", -1},
		"ConfirmVerificationCode":         {"aws", "cognito", "identity", "AWS Cognito", "VerifyUserAttribute", "", "ConfirmVerificationCode", -1},
		"SignOut":                         {"aws", "cognito", "identity", "AWS Cognito", "GlobalSignOut", "", "SignOut", -1},
		"UpdateUserAttribute":             {"aws", "cognito", "identity", "AWS Cognito", "AdminUpdateUserAttributes", "admin", "UpdateUserAttribute", -1},
		"ForgotPassword":                  {"aws", "cognito", "identity", "AWS Cognito", "ForgotPassword", "", "ForgotPassword", -1},
		"ConfirmForgotPassword":           {"aws", "cognito", "identity", "AWS Cognito", "ConfirmForgotPassword", "", "ConfirmForgotPassword", -1},
		"DisableUser":                     {"aws", "cognito", "identity", "AWS Cognito", "AdminDisableUser", "admin", "DisableUser", -1},
		"EnableUser":                      {"aws", "cognito", "identity", "AWS Cognito", "AdminEnableUser", "admin", "EnableUser", -1},
		"GetUser":                         {"aws", "cognito", "identity", "AWS Cognito", "AdminGetUser", "admin", "GetUser", -1},
		"GetUsers":                        {"aws", "cognito", "identity", "AWS Cognito", "ListUsers", "", "GetUsers", -1},
		"ChangePassword":                  {"aws", "cognito", "identity", "AWS Cognito", "ChangePassword", "", "ChangePassword", -1},
		"AdminUserGlobalSignOut":          {"aws", "cognito", "identity", "AWS Cognito", "AdminUserGlobalSignOut", "admin", "AdminUserGlobalSignOut", -1},
	},

	// ---- Phase 2: AWS SES (notification service) ----
	"github.com/tazapay/grpc-framework/client/email": {
		"SendRaw": {"aws", "ses", "email", "AWS SES", "SendRawEmail", "", "SendRaw", -1},
	},

	// ---- Phase 3: AWS S3 (9 services, via grpc-framework/client/storage) ----
	// DO NOT add InitS3 / SetS3 / SetPresign / SetWait / SanitizeFileName /
	// ValidateFileExtension / ValidateMIMEType — their absence is the allowlist guardrail.
	// BucketArgIndex -1 = bucket inside a struct arg (Upload/CopyObject); extraction deferred.
	"github.com/tazapay/grpc-framework/client/storage": {
		"Upload":        {"aws", "s3", "storage", "AWS S3", "PutObject",    "write",         "Upload",        -1},
		"GetObject":     {"aws", "s3", "storage", "AWS S3", "GetObject",    "read",          "GetObject",      1},
		"GetObjectURL":  {"aws", "s3", "storage", "AWS S3", "GetObject",    "presign-read",  "GetObjectURL",   1},
		"PutObjectURL":  {"aws", "s3", "storage", "AWS S3", "PutObject",    "presign-write", "PutObjectURL",   1},
		"HeadObject":    {"aws", "s3", "storage", "AWS S3", "HeadObject",   "metadata",      "HeadObject",     1},
		"IsObjectExists":{"aws", "s3", "storage", "AWS S3", "HeadObject",   "exists",        "IsObjectExists", 1},
		"Delete":        {"aws", "s3", "storage", "AWS S3", "DeleteObject", "delete",        "Delete",         1},
		"CopyObject":    {"aws", "s3", "storage", "AWS S3", "CopyObject",   "copy",          "CopyObject",    -1},
	},

	// ---- Phase 4 (SQS — do NOT add a competing detector; wire at the existing OutboxCall site) ----
}
