package architecture

// Writer/Message topic sensor (fleet escape E6, 2026-10-14).
//
//   - TestKafkaWriterTopicNotSetTwice: kafka-go rejects EVERY write whose
//     Message.Topic is set when the Writer has a Topic too ("kafka.(*Writer):
//     Topic must not be specified for both Writer and Message"). The escape
//     (slotting-optimization #25, warehouse-planning's domain DLQ writer): the
//     dead-letter writer was built with Topic: topic+DLQSuffix while
//     publishDeadLetter wrote kafkago.Message{Topic: dlqTopic, ...}, so no dead
//     letter ever landed and the partition wedged on the poison message.
//
//     Static form, per Go package (directory) of non-test files under
//     internal/ and cmd/: once ANY kafka-go Message literal in the package sets
//     Topic, every kafka-go Writer literal in that package must set no Topic,
//     unless the Writer literal carries `//kafka:fixed-topic-writer <reason>`
//     (on the line above it or on any of its lines) AND no Topic-setting
//     Message is provably written through it. "Provably" is resolved by the
//     variable/field the writer is bound to (x := &Writer{}, T{Field: ...},
//     recv.Field = ..., or a constructor that returns the literal, through its
//     call sites) against the writer each Topic-setting Message reaches
//     (w.WriteMessages(ctx, msg), msgs[i] = msg then w.WriteMessages(ctx,
//     msgs...), or a helper call writeDLQ(ctx, w, msg), following a parameter
//     one level back to the callers). When a binding cannot be resolved the
//     annotation alone is accepted. Writers configured after construction
//     (w.Topic = x) and kafka-go WriterConfig are not covered.
//   - TestKafkaWriterTopicSensorBites proves the detector flags the
//     warehouse-planning pre-fix shape (even when annotated) and passes the
//     fixed shape and a fixed-topic publisher beside a topic-free relay sink.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	wtopicKafkaGoImport = "github.com/segmentio/kafka-go"
	wtopicAnnotation    = "//kafka:fixed-topic-writer"
)

// wtopicWriter is one kafka-go Writer literal that sets a non-empty Topic.
type wtopicWriter struct {
	pos       string
	annotated bool
	keys      []string // bindings: "T.Field" or "Func.var"
	ctor      string   // the function that returns the literal, if any
}

// wtopicMessage is one kafka-go Message literal that sets Topic.
type wtopicMessage struct {
	pos     string
	targets []string // writer bindings it is written through; "param:F#i" before tracing
}

// wtopicPackage accumulates one package's findings across its files.
type wtopicPackage struct {
	writers  []wtopicWriter
	messages []wtopicMessage
	calls    map[string][]string // callee name -> binding keys of its call sites
	callArgs map[string][][]string
}

func newWtopicPackage() *wtopicPackage {
	return &wtopicPackage{calls: map[string][]string{}, callArgs: map[string][][]string{}}
}

// wtopicAlias returns the local name under which f imports kafka-go.
func wtopicAlias(f *ast.File) string {
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) != wtopicKafkaGoImport {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return "kafka"
	}
	return ""
}

// wtopicIsType reports whether typ names kafka-go's <name> under alias.
func wtopicIsType(typ ast.Expr, alias, name string) bool {
	if id, ok := typ.(*ast.Ident); ok {
		return alias == "." && id.Name == name
	}
	sel, ok := typ.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == alias
}

// wtopicTopicValue returns the Topic value of a literal, or nil.
func wtopicTopicValue(cl *ast.CompositeLit) ast.Expr {
	for _, e := range cl.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Topic" {
				return kv.Value
			}
		}
	}
	return nil
}

// wtopicSetsTopic reports whether cl sets a Topic that is not the literal "".
func wtopicSetsTopic(cl *ast.CompositeLit) bool {
	v := wtopicTopicValue(cl)
	if v == nil {
		return false
	}
	lit, ok := v.(*ast.BasicLit)
	return !ok || (lit.Value != `""` && lit.Value != "``")
}

// wtopicTypeName returns the bare type name of a composite literal type.
func wtopicTypeName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	case *ast.StarExpr:
		return wtopicTypeName(x.X)
	}
	return ""
}

// wtopicScope is the function a node sits in: its name, receiver, local
// variable types (x := &T{...}) and parameter positions.
type wtopicScope struct {
	name     string
	recvName string
	recvType string
	locals   map[string]string
	params   map[string]int
}

func newWtopicScope(fd *ast.FuncDecl) *wtopicScope {
	s := &wtopicScope{name: fd.Name.Name, locals: map[string]string{}, params: map[string]int{}}
	if fd.Recv != nil && len(fd.Recv.List) == 1 && len(fd.Recv.List[0].Names) == 1 {
		s.recvName = fd.Recv.List[0].Names[0].Name
		s.recvType = wtopicTypeName(fd.Recv.List[0].Type)
	}
	i := 0
	for _, fld := range fd.Type.Params.List {
		for _, n := range fld.Names {
			s.params[n.Name] = i
			i++
		}
		if len(fld.Names) == 0 {
			i++
		}
	}
	ast.Inspect(fd.Body, s.recordLocal)
	return s
}

// recordLocal records x := &T{...} / x := T{...} as a local of type T.
func (s *wtopicScope) recordLocal(n ast.Node) bool {
	as, ok := n.(*ast.AssignStmt)
	if !ok || as.Tok != token.DEFINE {
		return true
	}
	for i, rhs := range as.Rhs {
		id, isID := as.Lhs[i].(*ast.Ident)
		if u, isU := rhs.(*ast.UnaryExpr); isU {
			rhs = u.X
		}
		if cl, isCL := rhs.(*ast.CompositeLit); isID && isCL && cl.Type != nil && i < len(as.Lhs) {
			s.locals[id.Name] = wtopicTypeName(cl.Type)
		}
	}
	return true
}

// key resolves an expression naming a writer to a binding key, or "".
func (s *wtopicScope) key(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		if i, ok := s.params[x.Name]; ok {
			return fmt.Sprintf("param:%s#%d", s.name, i)
		}
		return s.name + "." + x.Name
	case *ast.SelectorExpr:
		return s.fieldKey(x)
	case *ast.ParenExpr:
		return s.key(x.X)
	case *ast.StarExpr:
		return s.key(x.X)
	}
	return ""
}

// fieldKey resolves recv.Field / local.Field to "T.Field".
func (s *wtopicScope) fieldKey(x *ast.SelectorExpr) string {
	id, ok := x.X.(*ast.Ident)
	if !ok {
		return ""
	}
	if id.Name == s.recvName && s.recvType != "" {
		return s.recvType + "." + x.Sel.Name
	}
	if t := s.locals[id.Name]; t != "" {
		return t + "." + x.Sel.Name
	}
	return ""
}

// binding returns the keys the value at the top of stack is bound to, and
// whether it is returned from the enclosing function.
func (s *wtopicScope) binding(stack []ast.Node) (keys []string, returned bool) {
	child, i := wtopicUnwrap(stack)
	if i < 0 {
		return nil, false
	}
	switch p := stack[i].(type) {
	case *ast.ReturnStmt:
		return nil, true
	case *ast.AssignStmt:
		for j, rhs := range p.Rhs {
			if rhs == child && j < len(p.Lhs) {
				return wtopicNonEmpty(s.key(p.Lhs[j])), false
			}
		}
	case *ast.ValueSpec:
		for _, n := range p.Names {
			keys = append(keys, s.name+"."+n.Name)
		}
	case *ast.KeyValueExpr:
		return s.compositeKey(stack[:i], p), false
	}
	return keys, false
}

// compositeKey resolves T{Field: v} to "T.Field".
func (s *wtopicScope) compositeKey(stack []ast.Node, kv *ast.KeyValueExpr) []string {
	k, ok := kv.Key.(*ast.Ident)
	if !ok || len(stack) == 0 {
		return nil
	}
	if cl, ok := stack[len(stack)-1].(*ast.CompositeLit); ok && cl.Type != nil {
		return []string{wtopicTypeName(cl.Type) + "." + k.Name}
	}
	return nil
}

// wtopicUnwrap skips & and ( ) parents of the top of stack: it returns the
// outermost wrapped expression and the index of its real parent.
func wtopicUnwrap(stack []ast.Node) (ast.Node, int) {
	i := len(stack) - 1
	child := stack[i]
	for i--; i >= 0; i-- {
		switch stack[i].(type) {
		case *ast.UnaryExpr, *ast.ParenExpr:
			child = stack[i]
			continue
		}
		return child, i
	}
	return child, -1
}

func wtopicNonEmpty(k string) []string {
	if k == "" {
		return nil
	}
	return []string{k}
}

// targets returns the writers a Topic-setting Message (top of stack) is
// written through, in fn's body.
func (s *wtopicScope) targets(stack []ast.Node, body *ast.BlockStmt) []string {
	child, i := wtopicUnwrap(stack)
	for i >= 0 {
		switch p := stack[i].(type) {
		case *ast.CompositeLit: // []kafkago.Message{msg, ...}
			child, i = p, i-1
			continue
		case *ast.CallExpr:
			if wtopicCalleeName(p) == "append" {
				child, i = p, i-1
				continue
			}
			return s.callTargets(p, child)
		case *ast.AssignStmt:
			return s.varTargets(p, child, body)
		case *ast.ValueSpec:
			return s.varTargetsNamed(p.Names, body)
		}
		return nil
	}
	return nil
}

// callTargets: w.WriteMessages(ctx, msg) -> w; helper(ctx, w, msg) -> every
// other argument.
func (s *wtopicScope) callTargets(call *ast.CallExpr, msg ast.Node) []string {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "WriteMessages" {
		return wtopicNonEmpty(s.key(sel.X))
	}
	var out []string
	for _, a := range call.Args {
		if a != msg {
			out = append(out, wtopicNonEmpty(s.key(a))...)
		}
	}
	return out
}

// varTargets: msgs[i] = msg / m := msg, then every WriteMessages call in
// body that passes that variable.
func (s *wtopicScope) varTargets(as *ast.AssignStmt, msg ast.Node, body *ast.BlockStmt) []string {
	for j, rhs := range as.Rhs {
		if rhs == msg && j < len(as.Lhs) {
			if id := wtopicRootIdent(as.Lhs[j]); id != nil {
				return s.varTargetsNamed([]*ast.Ident{id}, body)
			}
		}
	}
	return nil
}

func (s *wtopicScope) varTargetsNamed(names []*ast.Ident, body *ast.BlockStmt) []string {
	var out []string
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		for _, a := range call.Args {
			if id := wtopicRootIdent(a); id != nil && wtopicHasName(names, id.Name) {
				out = append(out, s.callTargets(call, a)...)
			}
		}
		return true
	})
	return out
}

func wtopicHasName(names []*ast.Ident, name string) bool {
	for _, n := range names {
		if n.Name == name {
			return true
		}
	}
	return false
}

func wtopicRootIdent(e ast.Expr) *ast.Ident {
	switch x := e.(type) {
	case *ast.Ident:
		return x
	case *ast.IndexExpr:
		return wtopicRootIdent(x.X)
	}
	return nil
}

func wtopicCalleeName(call *ast.CallExpr) string {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

// wtopicAnnotatedLines returns the lines carrying the fixed-topic annotation
// with a non-empty reason.
func wtopicAnnotatedLines(fset *token.FileSet, f *ast.File) map[int]bool {
	out := map[int]bool{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			rest, ok := strings.CutPrefix(c.Text, wtopicAnnotation)
			if ok && strings.TrimSpace(rest) != "" {
				out[fset.Position(c.Pos()).Line] = true
			}
		}
	}
	return out
}

func wtopicSpanAnnotated(fset *token.FileSet, n ast.Node, lines map[int]bool) bool {
	for l := fset.Position(n.Pos()).Line - 1; l <= fset.Position(n.End()).Line; l++ {
		if lines[l] {
			return true
		}
	}
	return false
}

// wtopicWalk visits every node under root with its ancestor stack (the node
// itself last).
func wtopicWalk(root ast.Node, visit func(stack []ast.Node)) {
	var stack []ast.Node
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		stack = append(stack, n)
		visit(stack)
		return true
	})
}

// addFile records f's Writer/Message literals and call-site bindings.
func (pkg *wtopicPackage) addFile(fset *token.FileSet, f *ast.File) {
	alias := wtopicAlias(f)
	if alias == "" || alias == "_" {
		return
	}
	lines := wtopicAnnotatedLines(fset, f)
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil {
			pkg.addFunc(fset, fd, alias, lines)
		}
	}
}

func (pkg *wtopicPackage) addFunc(fset *token.FileSet, fd *ast.FuncDecl, alias string, lines map[int]bool) {
	s := newWtopicScope(fd)
	wtopicWalk(fd.Body, func(stack []ast.Node) {
		switch n := stack[len(stack)-1].(type) {
		case *ast.CompositeLit:
			pkg.addLiteral(fset, s, fd.Body, stack, n, alias, lines)
		case *ast.CallExpr:
			pkg.addCall(s, stack, n)
		}
	})
}

func (pkg *wtopicPackage) addLiteral(fset *token.FileSet, s *wtopicScope, body *ast.BlockStmt, stack []ast.Node, cl *ast.CompositeLit, alias string, lines map[int]bool) {
	if cl.Type == nil || !wtopicSetsTopic(cl) {
		return
	}
	pos := fset.Position(cl.Pos()).String()
	if wtopicIsType(cl.Type, alias, "Message") {
		pkg.messages = append(pkg.messages, wtopicMessage{pos: pos, targets: s.targets(stack, body)})
		return
	}
	if !wtopicIsType(cl.Type, alias, "Writer") {
		return
	}
	w := wtopicWriter{pos: pos, annotated: wtopicSpanAnnotated(fset, cl, lines)}
	keys, returned := s.binding(stack)
	w.keys = keys
	if returned {
		w.ctor = s.name
	}
	pkg.writers = append(pkg.writers, w)
}

// addCall records the binding of a call site (DLQ: newDomainDLQWriter(b))
// and the binding of each of its arguments (for parameter tracing).
func (pkg *wtopicPackage) addCall(s *wtopicScope, stack []ast.Node, call *ast.CallExpr) {
	name := wtopicCalleeName(call)
	if name == "" {
		return
	}
	keys, _ := s.binding(stack)
	pkg.calls[name] = append(pkg.calls[name], keys...)
	args := make([]string, len(call.Args))
	for i, a := range call.Args {
		args[i] = s.key(a)
	}
	pkg.callArgs[name] = append(pkg.callArgs[name], args)
}

// wtopicParamKey splits "param:F#i" into F and i.
func wtopicParamKey(k string) (fn string, idx int, ok bool) {
	rest, ok := strings.CutPrefix(k, "param:")
	if !ok {
		return "", 0, false
	}
	fn, n, ok := strings.Cut(rest, "#")
	if !ok {
		return "", 0, false
	}
	idx, err := strconv.Atoi(n)
	return fn, idx, err == nil
}

// traced replaces "param:F#i" keys with the bindings of argument i at every
// call site of F (one level).
func (pkg *wtopicPackage) traced(keys []string) map[string]bool {
	out := map[string]bool{}
	for _, k := range keys {
		fn, idx, ok := wtopicParamKey(k)
		if !ok {
			out[k] = true
			continue
		}
		for _, args := range pkg.callArgs[fn] {
			if idx < len(args) && args[idx] != "" {
				out[args[idx]] = true
			}
		}
	}
	return out
}

// violations returns the package's rule breaches (see the file comment).
func (pkg *wtopicPackage) violations() []string {
	if len(pkg.messages) == 0 {
		return nil
	}
	var out []string
	for _, w := range pkg.writers {
		if !w.annotated {
			out = append(out, fmt.Sprintf("%s: Writer sets Topic while this package writes Message literals that set Topic (%s)", w.pos, pkg.messages[0].pos))
			continue
		}
		if m := pkg.writtenThrough(w); m != "" {
			out = append(out, fmt.Sprintf("%s: Writer annotated %s is written a Message that sets Topic at %s", w.pos, wtopicAnnotation, m))
		}
	}
	return out
}

// writtenThrough returns the position of a Topic-setting Message provably
// written through w, or "".
func (pkg *wtopicPackage) writtenThrough(w wtopicWriter) string {
	keys := append([]string{}, w.keys...)
	if w.ctor != "" {
		keys = append(keys, pkg.calls[w.ctor]...)
	}
	for _, m := range pkg.messages {
		reached := pkg.traced(m.targets)
		for _, k := range keys {
			if reached[k] {
				return m.pos
			}
		}
	}
	return ""
}

// wtopicPackages parses every non-test .go file under the given module-root
// dirs, grouped by package directory.
func wtopicPackages(t *testing.T, dirs ...string) map[string]*wtopicPackage {
	t.Helper()
	out := map[string]*wtopicPackage{}
	for _, d := range dirs {
		root := filepath.Join("..", "..", d)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		err := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			return wtopicAddPath(out, p)
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return out
}

func wtopicAddPath(pkgs map[string]*wtopicPackage, p string) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if pkgs[dir] == nil {
		pkgs[dir] = newWtopicPackage()
	}
	pkgs[dir].addFile(fset, f)
	return nil
}

func writerTopicViolation(what string) string {
	return fmt.Sprintf("ARCHITECTURE VIOLATION (contents rule)\n"+
		"  RULE:    a kafka-go Writer with a Topic is never written a Message that sets Topic\n"+
		"  WHAT:    %s\n"+
		"  WHY:     kafka-go rejects every such write (\"Topic must not be specified for both Writer and Message\"): "+
		"the dead letter or event never lands and the partition wedges (fleet escape E6, slotting #25).\n"+
		"  FIX:     drop Topic from the Writer and keep it on each Message; a Writer that only ever writes Messages "+
		"WITHOUT Topic may keep its fixed Topic with `%s <reason>` on or above the literal. Do NOT loosen this test.\n",
		what, wtopicAnnotation)
}

func TestKafkaWriterTopicNotSetTwice(t *testing.T) {
	pkgs := wtopicPackages(t, "internal", "cmd")
	dirs := make([]string, 0, len(pkgs))
	for d := range pkgs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		for _, v := range pkgs[d].violations() {
			t.Errorf("%s", writerTopicViolation(v))
		}
	}
}

// wtopicPreFix is warehouse-planning's domain dead-letter path before the E6
// fix: the writer has Topic: topic+DLQSuffix AND publishDeadLetter writes
// kafkago.Message{Topic: dlqTopic}, so kafka-go refused every dead letter.
const wtopicPreFix = `package kafka
import ("context"; kafkago "github.com/segmentio/kafka-go")
func newDomainDLQWriter(brokers []string, topic string) DeadLetterWriter {
	return &kafkago.Writer{
		Addr:         kafkago.TCP(brokers...),
		Topic:        topic + DLQSuffix,
		RequiredAcks: kafkago.RequireAll,
	}
}
func publishDeadLetter(ctx context.Context, w DeadLetterWriter, dlqTopic string, msg kafkago.Message, cause error) error {
	return writeDLQ(ctx, w, kafkago.Message{Topic: dlqTopic, Key: msg.Key, Value: msg.Value})
}
func writeDLQ(ctx context.Context, w DeadLetterWriter, msg kafkago.Message) error { return w.WriteMessages(ctx, msg) }
func NewLaborCapacityConsumer(brokers []string) *LaborCapacityConsumer {
	return &LaborCapacityConsumer{DLQ: newDomainDLQWriter(brokers, LaborTopic), DLQTopic: LaborTopic + DLQSuffix}
}
func (c *LaborCapacityConsumer) Run(ctx context.Context) error {
	loop := consumeLoop{}
	loop.deadLetter = func(ctx context.Context, msg kafkago.Message, cause error) error {
		return publishDeadLetter(ctx, c.DLQ, c.DLQTopic, msg, cause)
	}
	return loop.run(ctx)
}`

// wtopicFixed is the same code after the fix: the writer carries no Topic.
var wtopicFixed = strings.Replace(wtopicPreFix, "\t\tTopic:        topic + DLQSuffix,\n", "", 1)

// wtopicAnnotated is the pre-fix shape with the annotation added: it must
// still fail, because the Topic-setting Message provably reaches the writer.
var wtopicAnnotated = strings.Replace(wtopicPreFix, "\treturn &kafkago.Writer{",
	"\t//kafka:fixed-topic-writer the DLQ topic is fixed\n\treturn &kafkago.Writer{", 1)

// wtopicPublisherAndSink is a fixed-topic publisher next to a topic-free
// outbox relay sink (facility-layout, inventory-storage, ... outbound/kafka).
const wtopicPublisherAndSink = `package kafka
import ("context"; kafkago "github.com/segmentio/kafka-go")
func NewPublisher(brokers []string) *Publisher {
	return &Publisher{Writer: &kafkago.Writer{Addr: kafkago.TCP(brokers...), Topic: Topic, RequiredAcks: kafkago.RequireAll}} //ANN
}
func (p *Publisher) Publish(ctx context.Context, e Event) error {
	return p.Writer.WriteMessages(ctx, kafkago.Message{Key: e.Key, Value: e.Value})
}
func NewRelaySink(brokers []string) *RelaySink {
	return &RelaySink{w: &kafkago.Writer{Addr: kafkago.TCP(brokers...), RequiredAcks: kafkago.RequireAll}}
}
func (s *RelaySink) Send(ctx context.Context, rows []Row) error {
	msgs := make([]kafkago.Message, len(rows))
	for i, r := range rows {
		msgs[i] = kafkago.Message{Topic: r.Topic, Key: r.Key, Value: r.Value}
	}
	return s.w.WriteMessages(ctx, msgs...)
}`

type wtopicCase struct {
	name string
	src  string
	want int
	sub  string // a substring every reported violation must contain
}

func wtopicCases() []wtopicCase {
	return []wtopicCase{
		{"warehouse-planning pre-fix shape is flagged", wtopicPreFix, 1, "Writer sets Topic"},
		{"warehouse-planning fixed shape passes", wtopicFixed, 0, ""},
		{"annotating the pre-fix writer does not silence it", wtopicAnnotated, 1, "is written a Message that sets Topic"},
		{"unannotated fixed-topic publisher beside a Topic-setting relay sink is flagged", wtopicPublisherAndSink, 1, "Writer sets Topic"},
		{"annotated fixed-topic publisher beside a topic-free relay sink passes",
			strings.Replace(wtopicPublisherAndSink, "//ANN", "//kafka:fixed-topic-writer only Publish writes it, never with Topic", 1), 0, ""},
		{"an annotation without a reason does not count",
			strings.Replace(wtopicPublisherAndSink, "//ANN", "//kafka:fixed-topic-writer", 1), 1, "Writer sets Topic"},
		{"a local writer written a Topic-setting message is flagged even when annotated", `package x
import ("context"; kg "github.com/segmentio/kafka-go")
func f(ctx context.Context) error {
	w := &kg.Writer{Topic: "t"} //kafka:fixed-topic-writer local
	return w.WriteMessages(ctx, kg.Message{Topic: "u", Value: nil})
}`, 1, "is written a Message that sets Topic"},
		{"a Writer with an empty Topic is topic-free", `package x
import ("context"; kafkago "github.com/segmentio/kafka-go")
func f(ctx context.Context) error {
	w := &kafkago.Writer{Topic: ""}
	return w.WriteMessages(ctx, kafkago.Message{Topic: "u"})
}`, 0, ""},
		{"a package with no Topic-setting Message passes", `package x
import kafkago "github.com/segmentio/kafka-go"
func f() any { return &kafkago.Writer{Topic: "t.dlq"} }`, 0, ""},
	}
}

func TestKafkaWriterTopicSensorBites(t *testing.T) {
	if wtopicFixed == wtopicPreFix || wtopicAnnotated == wtopicPreFix {
		t.Fatal("fixture edit did not apply: a derived fixture equals the pre-fix one")
	}
	for _, tc := range wtopicCases() {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "src.go", tc.src, parser.ParseComments)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			pkg := newWtopicPackage()
			pkg.addFile(fset, f)
			got := pkg.violations()
			if len(got) != tc.want {
				t.Fatalf("violations=%v, want %d", got, tc.want)
			}
			for _, v := range got {
				if !strings.Contains(v, tc.sub) {
					t.Fatalf("violation %q does not say %q", v, tc.sub)
				}
			}
		})
	}
}
