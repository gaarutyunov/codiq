; Python stanza — tree-sitter query half (SPEC.md §5).
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
; This file says *where* things are. py.go says what they are called: it builds
; the SCIP descriptor suffix by walking each captured node's ancestors, assigns
; role and symbol_kind, and resolves same-file references. Anything that needs
; another file is emitted unresolved (§4.3).

; ---------------------------------------------------------------- scopes -----
;
; Python's lexical scopes and no others. This is the one place the Python stanza
; deliberately captures *less* than the TypeScript one: `(statement_block)` is a
; scope in TypeScript and `(block)` is not one in Python, because Python has no
; block scoping at all — a name bound inside an `if` is visible after it, in the
; enclosing function. Emitting a `block` scope would model something the
; language does not have, and py.go resolves same-file references by asking
; which scope a definition was declared in, so it would resolve them wrongly.
;
; Comprehensions are here because they genuinely are function scopes since
; Python 3: the loop variable of `[x for x in ys]` does not leak.

(module) @scope.file
(function_definition) @scope.function
(lambda) @scope.function
(list_comprehension) @scope.function
(set_comprehension) @scope.function
(dictionary_comprehension) @scope.function
(generator_expression) @scope.function
(class_definition) @scope.type

; ----------------------------------------------------------- definitions -----
;
; The file itself is the definition of a module, exactly as in TypeScript and
; for the same reason: Python writes no clause naming the module it is, so this
; is the one definition with no identifier to hang @name on and py.go names it
; from the coordinate and the path. It carries the neutral-core `package` kind
; because that is the kind link's `imports` derivation joins on.

(module) @definition.package

; One pattern for every `def`. A method is a `function_definition` inside a
; class body and nothing else — Python has no distinct node for it, where
; TypeScript has `method_definition` — so the distinction is drawn in py.go's
; refineKind from the enclosing container.
(function_definition name: (identifier) @name) @definition.function

; Likewise one pattern for every `class`. `interface` is not a Python keyword;
; refineKind promotes a class whose bases include Protocol or ABC, which is what
; declaring an interface in Python looks like and what link's `implements`
; derivation keys off.
(class_definition name: (identifier) @name) @definition.type

; `x = …`. Covers module-level names, class attributes and locals alike; which
; kind each is depends on the enclosing container, so refineKind decides.
(assignment left: (identifier) @name) @definition.variable

; `self.x = …`. This is how Python declares an instance attribute — there is no
; field declaration — so the assignment *is* the definition. py.go admits it
; only when the object is the method's own receiver, since `other.x = 1`
; declares nothing about the class this file is in.
(assignment left: (attribute object: (identifier) attribute: (identifier) @name)) @definition.field

; Binding forms that are not assignments.
(for_statement left: (identifier) @name) @definition.variable
(for_in_clause left: (identifier) @name) @definition.variable
(as_pattern alias: (as_pattern_target (identifier) @name)) @definition.variable

; Parameters, in every shape the grammar gives them. All are anchored under
; `parameters`/`lambda_parameters` so that a `*rest` in a tuple unpacking —
; which is the same node type — is not mistaken for one, and the structural
; capture sits on the parameter itself rather than on the list, so that py.go
; can read the annotation off it.
(parameters (identifier) @name @definition.parameter)
(parameters (typed_parameter (identifier) @name) @definition.parameter)
(parameters (default_parameter name: (identifier) @name) @definition.parameter)
(parameters (typed_default_parameter name: (identifier) @name) @definition.parameter)
(parameters (list_splat_pattern (identifier) @name) @definition.parameter)
(parameters (dictionary_splat_pattern (identifier) @name) @definition.parameter)
(lambda_parameters (identifier) @name @definition.parameter)
(lambda_parameters (default_parameter name: (identifier) @name) @definition.parameter)

; --------------------------------------------------------------- imports -----
;
; @name is the module the statement names. `import a, b` produces one match per
; name, which is what it should: two modules are imported and each gets its own
; occurrence. `from … import …` names its module once, and py.go reads the
; statement for the symbols the clause binds.

(import_statement name: (dotted_name) @name) @import
(import_statement name: (aliased_import) @name) @import
(import_from_statement module_name: (dotted_name) @name) @import
(import_from_statement module_name: (relative_import) @name) @import

; ------------------------------------------------------------ references -----
;
; A call and a plain read can capture the same identifier — `x.greet()` yields
; both a @reference.call and a @reference.read on `greet`. py.go dedupes by
; identifier range, preferring the more specific role, and drops any reference
; landing on a node a definition already claimed.
;
; Unlike TypeScript, both halves of a member expression are plain `identifier`
; nodes, so py.go tells the object side from the property side by comparing the
; captured node against the attribute's `object` field rather than by node type.

(call function: (identifier) @name) @reference.call
(call function: (attribute attribute: (identifier) @name)) @reference.call

(attribute object: (identifier) @name) @reference.read
(attribute attribute: (identifier) @name) @reference.read

; Annotations. `(type)` wraps every annotation position — parameter, return and
; variable — and `generic_type` is where the constructor of `list[int]` lives.
(type (identifier) @name) @reference.type
(generic_type (identifier) @name) @reference.type

; A base class is a reference to a type, and it is the reference `implements`
; would be derived from if the derivation read Python's syntax rather than the
; structural rule it applies to every language.
(class_definition superclasses: (argument_list (identifier) @name)) @reference.type
