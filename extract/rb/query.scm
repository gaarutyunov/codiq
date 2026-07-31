; Ruby stanza — tree-sitter query half (SPEC.md §5).
;
; Captures use the standard vocabulary shared by every language, which is where
; normalisation to the neutral core happens:
;
;   @definition.<kind>  a definition occurrence
;   @reference.<role>   a reference occurrence
;   @scope.<kind>       a lexical scope
;   @import             an import occurrence
;   @name               the identifier used for the descriptor and `name`
;
; This file says *where* things are. rb.go says what they are called: it builds
; the SCIP descriptor suffix by walking each captured node's ancestors, assigns
; role and symbol_kind, and resolves same-file references. Anything that needs
; another file is emitted unresolved (§4.3).
;
; Two properties of this grammar shape the whole file.
;
; The first is that Ruby's capitalisation rule is *lexical*, not conventional:
; `Foo` is a `constant` node and `foo` is an `identifier` node, decided by the
; tokeniser. That is the opposite of Java, whose stanza had to lean on a naming
; convention the JLS merely recommends, and it is why this query needs no
; convention at all to tell a constant path from a method call.
;
; The second is that the same rule buys nothing on the lowercase side. A
; receiverless, parenless `foo` is an `identifier` and nothing more — it is a
; local variable read or a method call on `self`, and the CST does not say which,
; because Ruby itself decides it from whether a local of that name has been
; assigned earlier in the same scope. So `(identifier)` is captured as a plain
; read and rb.go applies Ruby's own rule: a binding in scope wins, and anything
; else is a send to `self`.

; ---------------------------------------------------------------- scopes -----
;
; Ruby's lexical scopes, and deliberately no others. This is the Python stanza's
; restraint for the same reason: Ruby has no block scoping for locals — a name
; assigned inside an `if`, a `while`, a `case` or a `begin` is visible after it
; in the enclosing method — so capturing those as scopes would model something
; the language does not have, and rb.go resolves same-file references by asking
; which scope a definition was declared in.
;
; A `block`, a `do_block` and a `lambda` *are* scopes: their parameters are their
; own, and a local first assigned inside one does not leak out of it.
;
; `@scope.package` is the `module`, which is a real lexical region with `end` for
; a delimiter — the C# block-namespace case, one language on. `singleton_class`
; (`class << self`) is a type scope because that is exactly what it opens: the
; eigenclass.

(program) @scope.file
(module) @scope.package
(class) @scope.type
(singleton_class) @scope.type
(method) @scope.function
(singleton_method) @scope.function
(block) @scope.function
(do_block) @scope.function
(lambda) @scope.function

; ----------------------------------------------------------- definitions -----
;
; `module Foo` is captured as `@definition.package` and **never** as a module.
; The word is the obvious one — it is literally the keyword — and it is the wrong
; one: link's `imports` derivation joins on `symbol_kind = 'package'`
; (store/sqlc/query.sql), so anything emitted with the neutral core's `module`
; kind derives no edge and does so silently. Ruby is the language most likely to
; walk into that, which is why rb_test.go guards it by name.

(module name: (constant) @name) @definition.package
(class name: (constant) @name) @definition.type

; Callables. `def foo` and `def self.foo` are different members of different
; namespaces — Ruby lets one class declare both — so they are captured apart and
; rb.go gives the singleton form its own descriptor component.
;
; `name: (operator)` is absent on purpose, and it is C#'s reasoning verbatim:
; `def <=>`, `def []` and `def +` are reached by writing `a <=> b`, `x[i]` and
; `a + b`, none of which names the member it calls, so a definition-side
; descriptor for them would be one only the definition side could compute.
;
; `name: (setter)` *is* here — `def name=(v)` looks like the same problem and is
; not, because rb.go reconstructs the `=` on the reference side: `g.name = v`
; parses as an assignment whose left is a call, which is a syntactic fact and not
; a guess.
(method name: (identifier) @name) @definition.method
(method name: (setter) @name) @definition.method
(singleton_method name: (identifier) @name) @definition.method
(singleton_method name: (setter) @name) @definition.method

; `attr_reader :name` is a method call and it is also how Ruby declares public
; state; skipping it would leave `g.name` resolving to nothing in the majority of
; Ruby classes. The generated members are *methods* — Ruby has no field access at
; all, so `g.name` is a send however the member was declared — and rb.go emits one
; per symbol, plus the `name=` writer for the accessor and writer forms.
(call
  method: (identifier) @_attr
  arguments: (argument_list (simple_symbol) @name)) @definition.attr
  (#any-of? @_attr "attr_reader" "attr_writer" "attr_accessor")

; Constants. `MAX = 3` and `Point = Struct.new(:x)` are the same statement, and
; so is `class Foo` — a class *is* a constant bound to a Class — so the CST
; cannot separate a constant from a type and rb.go does not pretend it can.
(assignment left: (constant) @name) @definition.constant

; State and bindings. An instance or class variable's assignment is its
; declaration, exactly as `self.x = …` is in Python: Ruby has no field
; declaration form, so the write is the only place the member is written down.
(assignment left: (identifier) @name) @definition.variable
(assignment left: (instance_variable) @name) @definition.field
(assignment left: (class_variable) @name) @definition.field
(assignment left: (global_variable) @name) @definition.variable
(operator_assignment left: (identifier) @name) @definition.variable
(operator_assignment left: (instance_variable) @name) @definition.field
(operator_assignment left: (class_variable) @name) @definition.field
(operator_assignment left: (global_variable) @name) @definition.variable

; Parameters, in every shape the grammar gives them. The five wrapper nodes are
; unique to a parameter list, so they need no anchor; a plain `identifier` is not,
; so those three patterns are anchored on the list that owns them.
(method_parameters (identifier) @name) @definition.parameter
(block_parameters (identifier) @name) @definition.parameter
(lambda_parameters (identifier) @name) @definition.parameter
(optional_parameter name: (identifier) @name) @definition.parameter
(splat_parameter name: (identifier) @name) @definition.parameter
(hash_splat_parameter name: (identifier) @name) @definition.parameter
(keyword_parameter name: (identifier) @name) @definition.parameter
(block_parameter name: (identifier) @name) @definition.parameter
(exception_variable (identifier) @name) @definition.variable

; --------------------------------------------------------------- imports -----
;
; `require` and `require_relative` are ordinary method calls, and they are the
; only thing in Ruby that names another file. What they name is a *path* — not a
; namespace: Ruby's constants are one global tree that `require` merely populates,
; so a file's load-unit name and the namespace of the symbols it declares are
; two different things. rb.go models both, and this is the first half.
;
; The occurrence's descriptor is therefore the required path, which is
; byte-identical to the load-unit definition the required file emits for itself —
; and that is what link's `imports` derivation joins on.
(call
  method: (identifier) @_req
  arguments: (argument_list (string (string_content) @name))) @import
  (#any-of? @_req "require" "require_relative")

; ------------------------------------------------------------ references -----
;
; A call and a plain read can capture the same identifier — `g.greet` yields both
; a @reference.call on `greet` and, on `g`, a @reference.read. rb.go dedupes by
; identifier range, preferring the more specific role, and drops any reference
; landing on a node a definition already claimed.
;
; Only reads. `@reference.write` has no landing site in the core model: the
; occurrence table records role `definition` or `reference` and nothing else.

(call method: (identifier) @name) @reference.call
(call method: (constant) @name) @reference.call

; Constants, which are the whole of Ruby's type-reference story. There are no
; annotations to read, so every claim this stanza makes about a type comes from a
; constant written in an expression: a superclass, a rescued exception, a `when`
; pattern, a mixin argument, or the receiver of `Foo.new`. All of them are
; `constant` nodes and all are covered by the bare pattern.
;
; The two `scope_resolution` patterns are C#'s `qualified_name` pair one language
; on, and they exist for the same reason: the bare pattern sees `Greeter` and
; `Greeter` in `Greeter::Greeter` and cannot tell the namespace half from the type
; half, while the enclosing node saw the `::` and can. rb.go keeps the wider match
; where two land on one identifier.
(constant) @reference.type
(scope_resolution scope: (constant) @name) @reference.type
(scope_resolution name: (constant) @name) @reference.type

(identifier) @reference.read
(instance_variable) @reference.read
(class_variable) @reference.read
(global_variable) @reference.read
