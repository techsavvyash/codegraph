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
		"Upload":         {"aws", "s3", "storage", "AWS S3", "PutObject", "write", "Upload", -1},
		"GetObject":      {"aws", "s3", "storage", "AWS S3", "GetObject", "read", "GetObject", 1},
		"GetObjectURL":   {"aws", "s3", "storage", "AWS S3", "GetObject", "presign-read", "GetObjectURL", 1},
		"PutObjectURL":   {"aws", "s3", "storage", "AWS S3", "PutObject", "presign-write", "PutObjectURL", 1},
		"HeadObject":     {"aws", "s3", "storage", "AWS S3", "HeadObject", "metadata", "HeadObject", 1},
		"IsObjectExists": {"aws", "s3", "storage", "AWS S3", "HeadObject", "exists", "IsObjectExists", 1},
		"Delete":         {"aws", "s3", "storage", "AWS S3", "DeleteObject", "delete", "Delete", 1},
		"CopyObject":     {"aws", "s3", "storage", "AWS S3", "CopyObject", "copy", "CopyObject", -1},
	},

	// ---- Phase 4 (SQS — do NOT add a competing detector; wire at the existing OutboxCall site) ----

	// ---- P2-1: wrappers actually consumed across the fleet ----
	// Every package below uses the package-level global-client pattern (pkg.Func(...)),
	// so the detector matches by import path + func name exactly as the AWS wrappers do.
	// Constructors/config helpers (Init*/Set*/New*/With*) are deliberately excluded —
	// their absence is the allowlist guardrail. DO NOT add them.

	// featureflag — Unleash feature-flag evaluation (config sourced from AWS Secrets).
	// Only IsEnabled reaches the flag provider; Init/Close/With* are config/options.
	"github.com/tazapay/grpc-framework/client/featureflag": {
		"IsEnabled": {"unleash", "featureflag", "feature-flag", "Unleash Feature Flags", "IsEnabled", "", "IsEnabled", -1},
	},

	// kms — AWS KMS asymmetric key ops. DO NOT add Init/Set.
	"github.com/tazapay/grpc-framework/client/kms": {
		"GetPublicKey":        {"aws", "kms", "security", "AWS KMS", "GetPublicKey", "", "GetPublicKey", -1},
		"SignWithECDSASHA256": {"aws", "kms", "security", "AWS KMS", "Sign", "ECDSA_SHA_256", "SignWithECDSASHA256", -1},
	},

	// cloudfront — AWS CloudFront KeyValueStore (not signed URLs). DO NOT add
	// InitCloudfrontKeyValueStore/SetCloudfrontKeyValueStore.
	"github.com/tazapay/grpc-framework/client/cloudfront": {
		"PutKV": {"aws", "cloudfront-kvs", "cdn", "AWS CloudFront KeyValueStore", "PutKey", "", "PutKV", -1},
	},

	// webauthn — FIDO2 registration/authentication ceremonies (local crypto library,
	// go-webauthn). No network egress, but a real auth-capability boundary. DO NOT add
	// New/SetWebauthn or the User getter methods.
	"github.com/tazapay/grpc-framework/client/webauthn": {
		"BeginRegistration":       {"fido2", "webauthn", "auth", "WebAuthn/FIDO2", "BeginRegistration", "", "BeginRegistration", -1},
		"FinishRegistration":      {"fido2", "webauthn", "auth", "WebAuthn/FIDO2", "FinishRegistration", "", "FinishRegistration", -1},
		"BeginLogin":              {"fido2", "webauthn", "auth", "WebAuthn/FIDO2", "BeginLogin", "", "BeginLogin", -1},
		"FinishLogin":             {"fido2", "webauthn", "auth", "WebAuthn/FIDO2", "FinishLogin", "", "FinishLogin", -1},
		"BeginUsernamelessLogin":  {"fido2", "webauthn", "auth", "WebAuthn/FIDO2", "BeginLogin", "usernameless", "BeginUsernamelessLogin", -1},
		"FinishUsernamelessLogin": {"fido2", "webauthn", "auth", "WebAuthn/FIDO2", "FinishLogin", "usernameless", "FinishUsernamelessLogin", -1},
	},

	// sms — AWS SNS SMS operations. DO NOT add InitSNS/SetSMS. (PublishBatch has no
	// exported wrapper, so it is intentionally absent.)
	"github.com/tazapay/grpc-framework/client/sms": {
		"SendMessage":      {"aws", "sns", "messaging", "AWS SNS", "Publish", "sms", "SendMessage", -1},
		"IsNumberOptedOut": {"aws", "sns", "messaging", "AWS SNS", "CheckIfPhoneNumberIsOptedOut", "", "IsNumberOptedOut", -1},
		"OptInNumber":      {"aws", "sns", "messaging", "AWS SNS", "OptInPhoneNumber", "", "OptInNumber", -1},
		"GetSMSAttributes": {"aws", "sns", "messaging", "AWS SNS", "GetSMSAttributes", "", "GetSMSAttributes", -1},
	},

	// recaptcha — Google reCAPTCHA Enterprise assessment. DO NOT add SetClient.
	"github.com/tazapay/grpc-framework/client/recaptcha": {
		"CreateAssessment": {"google", "recaptcha", "security", "Google reCAPTCHA Enterprise", "CreateAssessment", "", "CreateAssessment", -1},
	},

	// chrome — headless Chrome via chromedp. Run drives the browser. DO NOT add Init/SetChrome.
	"github.com/tazapay/grpc-framework/client/chrome": {
		"Run": {"chromedp", "chrome", "browser", "Headless Chrome", "Run", "", "Run", -1},
	},

	// session — deliberately NOT registered: NewAWS() is a shared AWS config loader
	// (constructor category), performs no external operation of its own, and is consumed
	// internally by the other AWS wrappers. Adding it would be a false external call.
}
