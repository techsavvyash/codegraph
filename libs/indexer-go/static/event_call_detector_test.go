package static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	models "github.com/context-maximiser/code-graph/libs/core-models-go"
)

// parseFuncBody parses src and returns the first FuncDecl (used by event detector tests).
func parseFuncBodyForEvent(t *testing.T, src string) (*ast.FuncDecl, *token.FileSet) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			return fn, fset
		}
	}
	t.Fatal("no function found in source")
	return nil, nil
}

// newTestEventDetector creates an EventCallDetector with a nil client (safe for map-only ops).
func newTestEventDetector() *EventCallDetector {
	return NewEventCallDetector(nil, "test-service", models.DefaultScope())
}

// collectAssignStmtsFromEvent walks fn and returns all *ast.AssignStmt nodes.
func collectAssignStmtsFromEvent(fn *ast.FuncDecl) []*ast.AssignStmt {
	var stmts []*ast.AssignStmt
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if a, ok := n.(*ast.AssignStmt); ok {
			stmts = append(stmts, a)
		}
		return true
	})
	return stmts
}

// findCallByMethodName finds the first CallExpr where Fun is a SelectorExpr with the given method name.
func findCallByMethodName(fn *ast.FuncDecl, methodName string) *ast.CallExpr {
	var found *ast.CallExpr
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == methodName {
			found = c
		}
		return true
	})
	return found
}

// ── Helper function unit tests ────────────────────────────────────────────────

func TestExtractEventTypeArg(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "second arg is event type",
			src:  `package p; func f() { SaveOutboxEvent(ctx, "payment.created", payload) }`,
			want: "payment.created",
		},
		{
			name: "first arg is event type (no ctx)",
			src:  `package p; func f() { EmitEvent("order.placed", data) }`,
			want: "order.placed",
		},
		{
			name: "only ctx arg returns empty",
			src:  `package p; func f() { EmitEvent(ctx) }`,
			want: "",
		},
		{
			name: "no string arg returns empty",
			src:  `package p; func f() { EmitEvent(ctx, payload) }`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn, _ := parseFuncBodyForEvent(t, tt.src)
			// Find the call expression (deepest selector or ident call).
			var call *ast.CallExpr
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok && call == nil {
					call = c
				}
				return true
			})
			if call == nil {
				t.Fatal("no call expression found")
			}
			got := extractEventTypeArg(call)
			if got != tt.want {
				t.Errorf("extractEventTypeArg = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractQueueStringLiteral_SQSUrl(t *testing.T) {
	// extractQueueStringLiteral prefers strings matching "sqs.", "https://", or "arn:".
	src := `package p
func f() {
	sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    queueURL,
		MessageBody: &body,
	})
}`
	fn, _ := parseFuncBodyForEvent(t, src)
	call := findCallByMethodName(fn, "SendMessage")
	if call == nil {
		t.Fatal("SendMessage call not found")
	}
	// No string literal in this example — should return "".
	got := extractQueueStringLiteral(call)
	if got != "" {
		t.Errorf("expected empty string (no literal), got %q", got)
	}
}

func TestExtractQueueStringLiteral_LiteralURL(t *testing.T) {
	src := `package p
func f() {
	sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String("https://sqs.us-east-1.amazonaws.com/123/my-queue"),
		MessageBody: aws.String(body),
	})
}`
	fn, _ := parseFuncBodyForEvent(t, src)
	call := findCallByMethodName(fn, "SendMessage")
	if call == nil {
		t.Fatal("SendMessage call not found")
	}
	got := extractQueueStringLiteral(call)
	want := "https://sqs.us-east-1.amazonaws.com/123/my-queue"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExtractFirstStringLiteral(t *testing.T) {
	src := `package p
func f() {
	producer.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{Topic: &topicName},
		Value:          []byte("payload-data"),
	}, nil)
}`
	fn, _ := parseFuncBodyForEvent(t, src)
	call := findCallByMethodName(fn, "Produce")
	if call == nil {
		t.Fatal("Produce call not found")
	}
	got := extractFirstStringLiteral(call)
	// "payload-data" is the first string literal in the AST (Value field).
	if got == "" {
		t.Error("expected a string literal, got empty")
	}
}

func TestExtractFirstStringLiteral_TopicLiteral(t *testing.T) {
	src := `package p
func f() {
	writer.WriteMessages(ctx, kafka.Message{Topic: "payments.events", Value: payload})
}`
	fn, _ := parseFuncBodyForEvent(t, src)
	call := findCallByMethodName(fn, "WriteMessages")
	if call == nil {
		t.Fatal("WriteMessages call not found")
	}
	got := extractFirstStringLiteral(call)
	if got != "payments.events" {
		t.Errorf("got %q, want %q", got, "payments.events")
	}
}

// ── processTransportAssignment tests (no Neo4j needed) ───────────────────────

func TestProcessTransportAssignment_SQS(t *testing.T) {
	src := `package p
import "github.com/aws/aws-sdk-go/service/sqs"
func f() {
	sqsClient := sqs.New(sess)
}`
	fn, _ := parseFuncBodyForEvent(t, src)
	d := newTestEventDetector()

	for _, stmt := range collectAssignStmtsFromEvent(fn) {
		d.processTransportAssignment(stmt)
	}

	if tp, ok := d.varTransportMap["sqsClient"]; !ok || tp != "sqs" {
		t.Errorf("varTransportMap[sqsClient] = %q (ok=%v), want sqs", tp, ok)
	}
}

func TestProcessTransportAssignment_KafkaProducer_Constructor(t *testing.T) {
	src := `package p
import "github.com/confluentinc/confluent-kafka-go/kafka"
func f() {
	producer, _ := kafka.NewProducer(cfg)
}`
	fn, _ := parseFuncBodyForEvent(t, src)
	d := newTestEventDetector()

	for _, stmt := range collectAssignStmtsFromEvent(fn) {
		d.processTransportAssignment(stmt)
	}

	if tp, ok := d.varTransportMap["producer"]; !ok || tp != "kafka" {
		t.Errorf("varTransportMap[producer] = %q (ok=%v), want kafka", tp, ok)
	}
}

func TestProcessTransportAssignment_KafkaProducer_Literal(t *testing.T) {
	src := `package p
import "github.com/segmentio/kafka-go"
func f() {
	writer := &kafka.Writer{Addr: kafka.TCP("localhost:9092")}
}`
	fn, _ := parseFuncBodyForEvent(t, src)
	d := newTestEventDetector()

	for _, stmt := range collectAssignStmtsFromEvent(fn) {
		d.processTransportAssignment(stmt)
	}

	if tp, ok := d.varTransportMap["writer"]; !ok || tp != "kafka" {
		t.Errorf("varTransportMap[writer] = %q (ok=%v), want kafka", tp, ok)
	}
}

func TestProcessTransportAssignment_KafkaConsumer(t *testing.T) {
	src := `package p
import "github.com/confluentinc/confluent-kafka-go/kafka"
func f() {
	consumer, _ := kafka.NewConsumer(cfg)
}`
	fn, _ := parseFuncBodyForEvent(t, src)
	d := newTestEventDetector()

	for _, stmt := range collectAssignStmtsFromEvent(fn) {
		d.processTransportAssignment(stmt)
	}

	if tp, ok := d.varTransportMap["consumer"]; !ok || tp != "kafka-consumer" {
		t.Errorf("varTransportMap[consumer] = %q (ok=%v), want kafka-consumer", tp, ok)
	}
}

func TestProcessTransportAssignment_NATS(t *testing.T) {
	src := `package p
import "github.com/nats-io/nats.go"
func f() {
	nc, _ := nats.Connect("nats://localhost:4222")
}`
	fn, _ := parseFuncBodyForEvent(t, src)
	d := newTestEventDetector()

	for _, stmt := range collectAssignStmtsFromEvent(fn) {
		d.processTransportAssignment(stmt)
	}

	if tp, ok := d.varTransportMap["nc"]; !ok || tp != "nats" {
		t.Errorf("varTransportMap[nc] = %q (ok=%v), want nats", tp, ok)
	}
}

func TestProcessTransportAssignment_STANtoNATS(t *testing.T) {
	src := `package p
import "github.com/nats-io/stan.go"
func f() {
	sc, _ := stan.Connect("test-cluster", "client-id")
}`
	fn, _ := parseFuncBodyForEvent(t, src)
	d := newTestEventDetector()

	for _, stmt := range collectAssignStmtsFromEvent(fn) {
		d.processTransportAssignment(stmt)
	}

	if tp, ok := d.varTransportMap["sc"]; !ok || tp != "nats" {
		t.Errorf("varTransportMap[sc] = %q (ok=%v), want nats", tp, ok)
	}
}

func TestProcessTransportAssignment_VarMapResetPerFunction(t *testing.T) {
	src1 := `package p
func f1() { sqsClient := sqs.New(sess) }`
	fn1, _ := parseFuncBodyForEvent(t, src1)

	d := newTestEventDetector()
	for _, stmt := range collectAssignStmtsFromEvent(fn1) {
		d.processTransportAssignment(stmt)
	}
	if _, ok := d.varTransportMap["sqsClient"]; !ok {
		t.Fatal("expected sqsClient in varTransportMap after f1")
	}

	// Simulate the reset that happens at the top of DetectInFunction.
	d.varTransportMap = make(map[string]string)

	src2 := `package p
func f2() { x := 1; _ = x }`
	fn2, _ := parseFuncBodyForEvent(t, src2)
	for _, stmt := range collectAssignStmtsFromEvent(fn2) {
		d.processTransportAssignment(stmt)
	}

	if _, ok := d.varTransportMap["sqsClient"]; ok {
		t.Error("varTransportMap should not contain 'sqsClient' from previous function after reset")
	}
}

// ── Publish detection signal tests ────────────────────────────────────────────

func TestPublishDetection_OutboxFuncName(t *testing.T) {
	// Verify that outboxFuncNames covers the canonical function names.
	knownNames := []string{
		"SaveOutboxEvent", "InsertOutboxEvent", "AddOutboxEvent", "PublishOutboxEvent",
		"EnqueueOutboxEvent", "CreateOutboxEvent", "StoreOutboxEvent",
		"EmitEvent", "PublishEvent", "EnqueueEvent", "DispatchEvent",
	}
	for _, name := range knownNames {
		if !outboxFuncNames[name] {
			t.Errorf("outboxFuncNames missing %q", name)
		}
	}
}

func TestPublishDetection_SQSPublishMethods(t *testing.T) {
	for _, name := range []string{"SendMessage", "SendMessageWithContext", "SendMessageBatch"} {
		if !sqsPublishMethods[name] {
			t.Errorf("sqsPublishMethods missing %q", name)
		}
	}
}

func TestPublishDetection_KafkaProduceMethods(t *testing.T) {
	for _, name := range []string{"Produce", "WriteMessages", "WriteMessage"} {
		if !kafkaProduceMethods[name] {
			t.Errorf("kafkaProduceMethods missing %q", name)
		}
	}
}

func TestPublishDetection_NATSPublishMethods(t *testing.T) {
	for _, name := range []string{"Publish", "PublishMsg", "PublishRequest"} {
		if !natsPublishMethods[name] {
			t.Errorf("natsPublishMethods missing %q", name)
		}
	}
}
