package rb_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/codiq/coord"
	"github.com/gaarutyunov/codiq/extract/rb"
	"github.com/gaarutyunov/codiq/facts"
)

// testCoord is the coordinate the batch would hand the parser for a file in the
// gem codiq-greeter, rooted at the fixture directory.
func testCoord(t *testing.T) coord.Coord {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "greeter"))
	require.NoError(t, err)
	coords, err := coord.Resolve(root)
	require.NoError(t, err)
	c := coords.For("x" + rb.Ext)
	require.Equal(t, coord.RubyScheme, c.Scheme, "the fixture must resolve through the RubyGems resolver")
	return c
}

// parse parses src as the file at name, which is interpreted relative to the gem
// root. The path names the file's *load unit* and nothing else — a symbol's
// namespace is its constant nesting — and two tests below are about exactly that
// separation.
func parse(t *testing.T, name, src string) facts.FileFacts {
	t.Helper()
	c := testCoord(t)
	ff := rb.New().Parse(filepath.Join(c.Root, filepath.FromSlash(name)), []byte(src), c)
	require.Empty(t, ff.ParseError)
	return ff
}

const prefix = "scip-ruby gem codiq-greeter 1.0.0"

// --------------------------------------------------------------- definitions --

func TestParseDefinitionDescriptors(t *testing.T) {
	ff := parse(t, "lib/shapes/shapes.rb", `
module Com
  module Example
    module Shapes
      LEVEL = 1

      class Shapes
        attr_accessor :label

        def initialize(label)
          @label = label
          @@made = 0
        end

        def describe(times)
          local = @label
          local
        end

        def self.build(label)
          new(label)
        end
      end
    end
  end
end
`)

	for _, want := range []string{
		prefix + " Com/",
		prefix + " Com/Example/",
		prefix + " Com/Example/Shapes/",
		prefix + " Com/Example/Shapes/LEVEL#",
		prefix + " Com/Example/Shapes/Shapes#",
		prefix + " Com/Example/Shapes/Shapes#label().",
		prefix + " Com/Example/Shapes/Shapes#label=().",
		prefix + " Com/Example/Shapes/Shapes#initialize().",
		prefix + " Com/Example/Shapes/Shapes#initialize().(label)",
		prefix + " Com/Example/Shapes/Shapes#@label.",
		prefix + " Com/Example/Shapes/Shapes#@@made.",
		prefix + " Com/Example/Shapes/Shapes#describe().",
		prefix + " Com/Example/Shapes/Shapes#describe().(times)",
		prefix + " Com/Example/Shapes/Shapes#describe().local.",
		prefix + " Com/Example/Shapes/Shapes#self.build().",
		prefix + " Com/Example/Shapes/Shapes#self.build().(label)",
	} {
		assertHasDefinition(t, ff, want)
	}
}

func TestParseDefinitionKinds(t *testing.T) {
	ff := parse(t, "lib/kinds/kinds.rb", `
module Kinds
  MAX = 3

  class Thing
    attr_reader :label

    def initialize(label)
      @label = label
      @@made = 0
      $registry = nil
    end

    def describe(times)
      local = 1
      local
    end
  end
end

def loose(a)
  a
end
`)

	byName := definitionsByName(ff)
	tests := []struct {
		name string
		kind string
	}{
		{name: "Kinds", kind: facts.KindPackage},
		{name: "MAX", kind: facts.KindConstant},
		{name: "Thing", kind: facts.KindType},
		{name: "label", kind: facts.KindMethod},
		{name: "initialize", kind: facts.KindMethod},
		{name: "@label", kind: facts.KindField},
		{name: "@@made", kind: facts.KindField},
		{name: "$registry", kind: facts.KindVariable},
		{name: "describe", kind: facts.KindMethod},
		{name: "times", kind: facts.KindParameter},
		{name: "local", kind: facts.KindVariable},
		// A `def` outside every class and module is a private method of Object,
		// which is Ruby's way of saying "a procedure".
		{name: "loose", kind: facts.KindFunction},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := byName[tc.name]
			require.True(t, ok, "no definition named %q in %v", tc.name, definitionDescriptors(ff))
			assert.Equal(t, tc.kind, got.SymbolKind)
		})
	}
}

// TestNoDefinitionIsEmittedAsAModule guards the one kind that would be invisible
// to the link pass, and Ruby is the language most likely to trip on it: the
// keyword is literally `module`, so `facts.KindModule` reads as the obvious
// mapping and is wrong. `imports` joins on `symbol_kind = 'package'`
// (store/sqlc/query.sql), so a `module` kind derives no import edge at all and
// fails silently. §5's own capture list still names `module`; this is the reason
// not to follow it.
func TestNoDefinitionIsEmittedAsAModule(t *testing.T) {
	ff := parse(t, "lib/greeter/greeter.rb", `
module Com
  module Example
    module Greeting
      class Greeter
      end
    end
  end
end
`)
	for _, o := range ff.Occurrences {
		assert.NotEqual(t, facts.KindModule, o.SymbolKind,
			"%s was emitted as a module and link's imports derivation joins on package", o.Descriptor)
	}
	assert.Equal(t, facts.KindPackage, definitionsByName(ff)["Greeting"].SymbolKind)
	assert.Equal(t, prefix+" Com/Example/Greeting/",
		definitionsByName(ff)["Greeting"].Descriptor.String())
}

// TestAMixinIsAPackageReference is the other half of the same decision, and it is
// what makes it pay: `include Foo` names a module, so it is recorded as a
// reference to that module's *package* descriptor — byte-identical to what the
// declaring file's `module Foo` renders, which is what link's `imports`
// derivation joins on.
func TestAMixinIsAPackageReference(t *testing.T) {
	for _, keyword := range []string{"include", "extend", "prepend"} {
		t.Run(keyword, func(t *testing.T) {
			ff := parse(t, "lib/loud/loud.rb", `
module Loud
end

class Speaker
  `+keyword+` Loud
end
`)
			ref := referenceNamed(t, ff, "Loud")
			assert.Equal(t, facts.KindPackage, ref.SymbolKind)
			assert.Equal(t, prefix+" Loud/", ref.Descriptor.String())
		})
	}
}

// TestNoInterfaceIsEverEmitted states the `implements` decision as a fact about
// the output rather than as a paragraph.
//
// link derives `implements` from method-set containment and keys it off
// `symbol_kind = 'interface'`. Ruby has no interfaces, and the nearest thing —
// a module included as a mixin — is the *inverse* of one: `Comparable` gives six
// methods and demands `<=>`, so a class that includes it declares the one method
// the module does not have and none of the six it does. Containment therefore
// fails exactly where the include is real, and the only place it could succeed is
// a class that happens to share a module's method names and included nothing,
// which is duck typing manufacturing a claim nobody made.
//
// So the module stays a `package`, whose descriptor ends `/` and which link's
// `type_def` CTE (`right(descriptor, 1) = '#'`) does not select, and Ruby derives
// no `implements` edges by construction.
func TestNoInterfaceIsEverEmitted(t *testing.T) {
	ff := parse(t, "lib/speaker/speaker.rb", `
module Speakable
  def announce
    greet.upcase
  end
end

class Speaker
  include Speakable

  def greet
    "hi"
  end
end
`)
	for _, o := range ff.Occurrences {
		assert.NotEqual(t, facts.KindInterface, o.SymbolKind,
			"%s was emitted as an interface; Ruby has none and link keys implements off the kind", o.Descriptor)
	}
	// The shape link would need and does not get: a module's members hang off a
	// `/` descriptor, which no type_def selects.
	assertHasDefinition(t, ff, prefix+" Speakable/announce().")
	assertHasDefinition(t, ff, prefix+" Speaker#greet().")
}

// TestSingletonMethodsAreADistinctMemberNamespace is the descriptor decision Ruby
// forced, and the reason is cs.go's own test applied one language on: an explicit
// interface implementation drops its qualifier because no reference site could
// reconstruct it, and a singleton method keeps one because every reference site
// can. `Greeter.build` has a constant for a receiver, which names the class
// object; `g.build` has a value. The syntax separates them, so the descriptor may.
//
// It also has to: C# renders a static and an instance member alike and can afford
// to, because the language forbids a type declaring both. Ruby permits exactly
// that, and the two are different methods.
func TestSingletonMethodsAreADistinctMemberNamespace(t *testing.T) {
	ff := parse(t, "lib/greeter/greeter.rb", `
class Greeter
  def build
    "instance"
  end

  def self.build
    "singleton"
  end

  class << self
    def default
      build
    end
  end
end
`)
	assertHasDefinition(t, ff, prefix+" Greeter#build().")
	assertHasDefinition(t, ff, prefix+" Greeter#self.build().")
	// `class << self` opens the same eigenclass `def self.…` writes into, so the
	// two spellings have to render the same component — and the receiverless
	// `build` inside it resolves to the singleton and not to the instance method.
	assertHasDefinition(t, ff, prefix+" Greeter#self.default().")
	assertResolvesLocally(t, ff, "build", prefix+" Greeter#self.build().")
}

// TestASingletonCallIsReconstructedFromTheReceiver is the claim that makes the
// component above legitimate rather than merely tidy.
func TestASingletonCallIsReconstructedFromTheReceiver(t *testing.T) {
	ff := parse(t, "lib/app/app.rb", `
class Greeter
  def self.build
    "x"
  end

  def greet
    "y"
  end
end

class App
  def run
    Greeter.build
    g = Greeter.new
    g.greet
  end
end
`)
	assertResolvesLocally(t, ff, "build", prefix+" Greeter#self.build().")
	assertResolvesLocally(t, ff, "greet", prefix+" Greeter#greet().")
	// `new` is `Class#new` and is not this class's, so it lands on the singleton
	// namespace with nothing behind it — an unresolved reference and not `initialize`.
	assert.Equal(t, prefix+" Greeter#self.new().", referenceNamed(t, ff, "new").Descriptor.String())
}

// TestAttrDeclarationsAreMethods is the one Ruby idiom a stanza cannot skip:
// there is no field access in the language, so `g.name` is a send whether the
// member was written with `def` or generated by `attr_reader`, and the majority of
// Ruby classes declare their state the second way.
func TestAttrDeclarationsAreMethods(t *testing.T) {
	ff := parse(t, "lib/state/state.rb", `
class State
  attr_reader :one, :two
  attr_writer :three
  attr_accessor :four
end
`)
	for _, want := range []string{
		prefix + " State#one().",
		prefix + " State#two().",
		prefix + " State#three=().",
		prefix + " State#four().",
		prefix + " State#four=().",
	} {
		assertHasDefinition(t, ff, want)
	}
	assert.Equal(t, facts.KindMethod, definitionsByName(ff)["one"].SymbolKind)
}

// TestASetterIsReachableFromAnAssignment is why `def name=(v)` is a definition at
// all while `def <=>` is not. Both look unreconstructable and only one is: an
// assignment whose left-hand side is a call names the writer, which is a
// syntactic fact this stanza reads rather than a convention it assumes.
func TestASetterIsReachableFromAnAssignment(t *testing.T) {
	ff := parse(t, "lib/state/state.rb", `
class State
  def label=(v)
    @label = v
  end

  def rename
    self.label = "x"
  end
end
`)
	assertHasDefinition(t, ff, prefix+" State#label=().")
	assertResolvesLocally(t, ff, "label", prefix+" State#label=().")
}

// TestAnOperatorMethodIsNotADefinition is the other side of that test, and it is
// C#'s reasoning verbatim: `a <=> b` and `x[i]` name no member, so a descriptor
// for one would be computable only from the declaration.
func TestAnOperatorMethodIsNotADefinition(t *testing.T) {
	ff := parse(t, "lib/state/state.rb", `
class State
  def <=>(other)
    0
  end

  def [](index)
    index
  end
end
`)
	assert.NotContains(t, definitionDescriptors(ff), prefix+" State#<=>().")
	assert.NotContains(t, definitionDescriptors(ff), prefix+" State#[]().")
	// The parameters are still inside the method they were written in, even
	// though the method itself has no descriptor of its own — the alternative
	// hangs them off the enclosing class as though the `def` were not there.
	assertHasDefinition(t, ff, prefix+" State#<=>().(other)")
	assertHasDefinition(t, ff, prefix+" State#[]().(index)")
}

// ----------------------------------------------------------------- namespace --

// TestTheNamespaceIsTheConstantNestingAndNotThePath is the decision this stanza
// turns on, and the fixture layout is what makes it checkable: the same source
// written at three different paths renders one descriptor, because a Ruby file's
// path names a load unit and never a namespace.
//
// It is also what a reference can reconstruct. `Com::Example::Greeting::Greeter`
// written three directories away has to render what the declaration rendered, and
// the constant path is the only thing both sides can compute.
func TestTheNamespaceIsTheConstantNestingAndNotThePath(t *testing.T) {
	src := `
module Com
  module Example
    module Greeting
      class Greeter
        def greet
          "hi"
        end
      end
    end
  end
end
`
	for _, path := range []string{
		"lib/greeter/greeter.rb",
		"app/models/anything.rb",
		"greeter.rb",
	} {
		t.Run(path, func(t *testing.T) {
			ff := parse(t, path, src)
			assertHasDefinition(t, ff, prefix+" Com/Example/Greeting/Greeter#greet().")
		})
	}
}

// TestReopeningRendersOneDescriptor is Ruby's version of C#'s partial class, and
// it is the harder case: a reopening needs no keyword, happens across
// directories, and is how ordinary Ruby is written rather than a code
// generator's convention.
//
// Two definition rows carrying one descriptor are two *sites of one symbol*, and
// that is what the link pass wants rather than something it survives — a class
// genuinely is declared in both files. It holds here because the namespace is the
// constant nesting: a path-derived namespace would have rendered `lib/Loud#` and
// `core_ext/Loud#` and split one class into two symbols.
func TestReopeningRendersOneDescriptor(t *testing.T) {
	shout := parse(t, "lib/loud_shout.rb", `
class Loud
  def shout
    "HELLO"
  end
end
`)
	whisper := parse(t, "core_ext/loud_whisper.rb", `
class Loud
  def whisper
    "hello"
  end
end
`)
	assertHasDefinition(t, shout, prefix+" Loud#")
	assertHasDefinition(t, whisper, prefix+" Loud#")
	assertHasDefinition(t, shout, prefix+" Loud#shout().")
	assertHasDefinition(t, whisper, prefix+" Loud#whisper().")
}

// TestReopeningACoreClassStaysInThisRepository is the case C# has no analogue
// for, and the decision is stated here because it is not obvious.
//
// A monkey patch could be given a foreign coordinate, as py.go gives one to
// `builtins` and cs.go to `System`. It is not, because a foreign coordinate is
// shared by every Ruby repository in an index: two unrelated patches of `String`
// would render one descriptor and the link pass would materialise an edge between
// them. This coordinate can only collide with another reopening of `String` in
// the same repository, which is the same class.
//
// What it costs is that the patch is a definition with nothing pointing at it,
// since a receiver's type is unknowable file-locally — an orphan, which is honest.
func TestReopeningACoreClassStaysInThisRepository(t *testing.T) {
	ff := parse(t, "core_ext/string.rb", `
class String
  def blank?
    strip.empty?
  end
end
`)
	assertHasDefinition(t, ff, prefix+" String#")
	assertHasDefinition(t, ff, prefix+" String#blank?().")
}

// TestAGlobalVariableHasNoContainer records the one binding form Ruby puts
// outside every namespace: `$x` is the same variable however deeply nested the
// assignment is.
func TestAGlobalVariableHasNoContainer(t *testing.T) {
	ff := parse(t, "lib/g.rb", `
module Deep
  class Deeper
    def set
      $registry = 1
    end
  end
end
`)
	assertHasDefinition(t, ff, prefix+" $registry.")
}

// ------------------------------------------------------------------- imports --

// TestTheLoadUnitIsThePath is the second of Ruby's two units of modularity, and
// the one place a path is allowed into a descriptor. A `require` names a file and
// nothing else, so the thing it refers to has to be keyed by path.
func TestTheLoadUnitIsThePath(t *testing.T) {
	ff := parse(t, "greeter/greeter.rb", "class Greeter\nend\n")
	unit := unitDefinition(t, ff)
	assert.Equal(t, prefix+" greeter/greeter/", unit.Descriptor.String())
	assert.Equal(t, facts.KindPackage, unit.SymbolKind)
	assert.Equal(t, "greeter", unit.Name)
	// Zero-width at the start of the file: there is no identifier to point at,
	// and a range spanning the file would claim every other occurrence sits
	// inside the unit's name.
	assert.Equal(t, 0, unit.RangeStart)
	assert.Equal(t, 0, unit.RangeEnd)
}

func TestRequireResolution(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		src      string
		wantName string
		want     string
	}{
		{
			// The join that makes `imports` work: this is byte-identical to what
			// `greeter/greeter.rb` writes for its own load unit.
			name:     "a plain require is read from the repository root",
			path:     "app/program.rb",
			src:      `require "greeter/greeter"`,
			wantName: "greeter",
			want:     prefix + " greeter/greeter/",
		},
		{
			name:     "a require_relative is path arithmetic against this file",
			path:     "app/program.rb",
			src:      `require_relative "../greeter/greeter"`,
			wantName: "greeter",
			want:     prefix + " greeter/greeter/",
		},
		{
			name:     "a sibling require_relative",
			path:     "app/program.rb",
			src:      `require_relative "helper"`,
			wantName: "helper",
			want:     prefix + " app/helper/",
		},
		{
			// A `.rb` suffix is legal and unusual, and both spellings name the
			// same file, so both have to render the same descriptor.
			name:     "an explicit extension is dropped",
			path:     "app/program.rb",
			src:      `require_relative "helper.rb"`,
			wantName: "helper",
			want:     prefix + " app/helper/",
		},
		{
			// Ruby's own, and never this tree's: a foreign coordinate cannot match
			// a file this index owns even if the repository holds a `json.rb`.
			name:     "a standard-library feature is foreign",
			path:     "app/program.rb",
			src:      `require "json"`,
			wantName: "json",
			want:     "scip-ruby gem json .",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := parse(t, tc.path, tc.src+"\n")
			ref := referenceNamed(t, ff, tc.wantName)
			assert.Equal(t, facts.KindPackage, ref.SymbolKind)
			assert.Equal(t, tc.want, ref.Descriptor.String())
		})
	}
}

// TestARequireIsNotAlsoACall is the dedupe that keeps a directive from describing
// the same bytes twice. `require`, `include` and `attr_accessor` are method calls
// in the language and declarations in this model, and what each of them means is
// carried by the path, the module or the member it names.
func TestARequireIsNotAlsoACall(t *testing.T) {
	ff := parse(t, "app/program.rb", "require \"greeter/greeter\"\ninclude Enumerable\nattr_reader :x\n")
	for _, o := range ff.Occurrences {
		assert.NotContains(t, []string{"require", "include", "attr_reader"}, o.Name,
			"%s was emitted as an ordinary send", o.Descriptor)
	}
}

// ---------------------------------------------------------------- references --

func TestParseReferenceDescriptors(t *testing.T) {
	ff := parse(t, "app/program.rb", `
module App
  class Program
    def run
      g = Com::Example::Greeting::Greeter.new("world")
      g.greet
    end
  end
end
`)
	for _, want := range []string{
		// Every segment but the last is a namespace, which is what makes a
		// qualified path derive an `imports` edge to the file declaring it.
		prefix + " Com/",
		prefix + " Com/Example/",
		prefix + " Com/Example/Greeting/",
		prefix + " Com/Example/Greeting/Greeter#",
		prefix + " Com/Example/Greeting/Greeter#self.new().",
		prefix + " Com/Example/Greeting/Greeter#greet().",
	} {
		assert.Contains(t, referenceDescriptors(ff), want)
	}
}

// TestAReceiverIsResolvedThroughItsConstruction is the whole of Ruby's type
// recovery: there are no annotations, so `C.new` is the only expression whose
// result the source names.
func TestAReceiverIsResolvedThroughItsConstruction(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a local initialised from a constant",
			src:  "g = Greeter.new\ng.greet",
			want: prefix + " Greeter#greet().",
		},
		{
			name: "a local initialised from a qualified constant",
			src:  "g = Deep::Greeter.new\ng.greet",
			want: prefix + " Deep/Greeter#greet().",
		},
		{
			// An instance variable belongs to the class and not to the method that
			// first assigned it, so the type is remembered file-wide.
			name: "an instance variable initialised in another method",
			src:  "def setup\n  @g = Greeter.new\nend\n\ndef use\n  @g.greet\nend",
			want: prefix + " Greeter#greet().",
		},
		{
			// A factory is not a constructor. `Greeter.build` may return anything,
			// and a name is not a contract, so the receiver stays unknown and the
			// descriptor writes SCIP's "." rather than a guess.
			name: "a factory is not read",
			src:  "g = Greeter.build\ng.greet",
			want: prefix + " .#greet().",
		},
		{
			// Nothing at all is known about the receiver.
			name: "an unknown receiver",
			src:  "g = something\ng.greet",
			want: prefix + " .#greet().",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ff := parse(t, "app/program.rb", "class Program\n  def run\n    "+tc.src+"\n  end\nend\n")
			assert.Contains(t, referenceDescriptors(ff), tc.want)
		})
	}
}

// TestABareNameIsALocalOrASendToSelf is the one place Ruby's lexical
// capitalisation rule buys nothing, and the rule this stanza applies is Ruby's
// own: a local assigned earlier in an enclosing scope wins, and everything else
// is a send to `self`.
func TestABareNameIsALocalOrASendToSelf(t *testing.T) {
	ff := parse(t, "lib/greeter/greeter.rb", `
class Greeter
  def greet
    "hi"
  end

  def loud
    text = greet
    text
  end
end
`)
	// `greet` was never assigned, so it is a send; `text` was, so it is a read of
	// the binding.
	assertResolvesLocally(t, ff, "greet", prefix+" Greeter#greet().")
	assert.Equal(t, prefix+" Greeter#loud().text.",
		referenceNamed(t, ff, "text").Descriptor.String())
}

// TestAnUnqualifiedConstantResolvesThroughTheEnclosingModules is the fallback for
// the half of Ruby's constant lookup a file-local reader can follow, and it is
// deliberately the *modules* rather than every container. Two classes in one
// module are siblings, so a constant written in one of them names the module's
// member far more often than its own.
func TestAnUnqualifiedConstantResolvesThroughTheEnclosingModules(t *testing.T) {
	ff := parse(t, "lib/greeter/greeter.rb", `
module Greeting
  class Greeter
    def build
      Formatter.new
    end
  end
end
`)
	assert.Equal(t, prefix+" Greeting/Formatter#",
		referenceNamed(t, ff, "Formatter").Descriptor.String())
}

// TestAQualifiedConstantIsAbsolute is the other half, and it is cs.go's split for
// cs.go's reason: `::` is written precisely because the constant is not the one
// the enclosing nesting would find, while a bare name is written because it is.
func TestAQualifiedConstantIsAbsolute(t *testing.T) {
	ff := parse(t, "app/program.rb", `
module App
  class Program
    def run
      Greeting::Formatter.new
    end
  end
end
`)
	assert.Equal(t, prefix+" Greeting/Formatter#",
		referenceNamed(t, ff, "Formatter").Descriptor.String())
	assert.Equal(t, prefix+" Greeting/",
		referenceNamed(t, ff, "Greeting").Descriptor.String())
}

// TestALocallyDeclaredClassUsedAsANamespaceIsFound is the rescue for the one
// reading `::` leaves open. A path segment before a `::` may be a module or a
// class and the syntax does not say which; it is read as a module because that is
// what namespacing constants nearly always are, and a class used that way is
// found anyway when this file declares it.
func TestALocallyDeclaredClassUsedAsANamespaceIsFound(t *testing.T) {
	ff := parse(t, "lib/registry/registry.rb", `
class Registry
  class Entry
    def label
      "x"
    end
  end
end

class Lookup
  def find
    Registry::Entry.new.label
  end
end
`)
	assertHasDefinition(t, ff, prefix+" Registry#Entry#label().")
	assert.Equal(t, prefix+" Registry#Entry#",
		referenceNamed(t, ff, "Entry").Descriptor.String())
	assertResolvesLocally(t, ff, "label", prefix+" Registry#Entry#label().")
}

// TestARootAnchoredPathDropsItsLeadingSeparator records that `::Top` and `Top`
// name the same constant once a qualified path is read from the root, which this
// stanza does either way.
func TestARootAnchoredPathDropsItsLeadingSeparator(t *testing.T) {
	ff := parse(t, "app/program.rb", "x = ::Top::Thing.new\n")
	assert.Contains(t, referenceDescriptors(ff), prefix+" Top/Thing#")
	assert.Contains(t, referenceDescriptors(ff), prefix+" Top/")
}

// ------------------------------------------------------------------- scopes ---

// TestScopesAreRubysOwn is the restraint py.go established: Ruby has no block
// scoping for locals, so `if`, `while`, `case` and `begin` are not scopes and
// emitting them would model something the language does not have. A `block` and a
// `lambda` are, because their parameters are their own.
func TestScopesAreRubysOwn(t *testing.T) {
	ff := parse(t, "lib/scopes/scopes.rb", `
module Outer
  class Inner
    def method
      if true
        x = 1
      end
      [1].each { |i| i }
      [1].each do |j|
        j
      end
      ->(k) { k }
      class << self
        def eigen
          1
        end
      end
    end
  end
end
`)
	kinds := map[string]int{}
	for _, s := range ff.Scopes {
		kinds[s.Kind]++
	}
	assert.Equal(t, 1, kinds[facts.ScopeFile])
	assert.Equal(t, 1, kinds[facts.ScopePackage], "a module is a lexical region and is a scope")
	assert.Equal(t, 2, kinds[facts.ScopeType], "a class and an eigenclass")
	// Six and not five: `->(k) { k }` is a `lambda` whose body is a `block`, and
	// both are captured — the parameters hang off the first and the bindings off
	// the second, so the two are genuinely nested rather than duplicated.
	assert.Equal(t, 6, kinds[facts.ScopeFunction], "two defs, a brace block, a do block, and a lambda over its own block")
	assert.Zero(t, kinds[facts.ScopeBlock], "Ruby has no block scoping for locals")
}

func TestParseResolvesSameFileReferences(t *testing.T) {
	ff := parse(t, "lib/greeter/greeter.rb", `
module Greeting
  GREETING = "hello"

  class Greeter
    def initialize(name)
      @name = name
    end

    def greet
      GREETING + @name
    end
  end
end
`)
	assertResolvesLocally(t, ff, "GREETING", prefix+" Greeting/GREETING#")
	assertResolvesLocally(t, ff, "@name", prefix+" Greeting/Greeter#@name.")
	assertResolvesLocally(t, ff, "name", prefix+" Greeting/Greeter#initialize().(name)")
}

func TestParseEmitsOnlyExtractableEdgeKinds(t *testing.T) {
	ff := parse(t, "lib/greeter/greeter.rb", `
class Greeter
  def greet
    greet
  end
end
`)
	allowed := map[facts.EdgeKind]bool{
		facts.EdgeDefines: true, facts.EdgeContains: true, facts.EdgeReferencesLocal: true,
	}
	for _, e := range ff.Edges {
		assert.True(t, allowed[e.Kind], "extracted edge kind %q is the link pass's", e.Kind)
	}
}

// ------------------------------------------------------------------- parsing --

func TestParseFile(t *testing.T) {
	c := testCoord(t)
	path := filepath.Join(c.Root, "app", "program.rb")
	ff := rb.New().Parse(path, []byte("module A\n  class P\n  end\nend\n"), c)

	require.Empty(t, ff.ParseError)
	assert.Equal(t, path, ff.File.Path)
	assert.Equal(t, rb.Lang, ff.File.Lang)
	assert.Equal(t, c, ff.File.Coord)
}

func TestParseBrokenSourceStillReturns(t *testing.T) {
	c := testCoord(t)
	ff := rb.New().Parse(filepath.Join(c.Root, "app", "broken.rb"), []byte("class ;;; def"), c)
	assert.Equal(t, rb.Lang, ff.File.Lang)
}

// TestParseEmptyFile records what an empty Ruby file is: a load unit and nothing
// else. It declares no constant, so it has no namespace to define.
func TestParseEmptyFile(t *testing.T) {
	ff := parse(t, "app/empty.rb", "")
	assert.Equal(t, prefix+" app/empty/", unitDefinition(t, ff).Descriptor.String())
	assert.Len(t, ff.Occurrences, 1)
}

// TestParseTheFixture is the corpus the feature suite indexes, checked here so
// that a change to either side is caught by a unit test rather than by a
// container.
func TestParseTheFixture(t *testing.T) {
	c := testCoord(t)

	greeterPath := filepath.Join(c.Root, "greeter", "greeter.rb")
	greeterSrc, err := os.ReadFile(greeterPath) //nolint:gosec // the fixture.
	require.NoError(t, err)
	greeter := rb.New().Parse(greeterPath, greeterSrc, c)
	require.Empty(t, greeter.ParseError)

	programPath := filepath.Join(c.Root, "app", "program.rb")
	programSrc, err := os.ReadFile(programPath) //nolint:gosec // the fixture.
	require.NoError(t, err)
	program := rb.New().Parse(programPath, programSrc, c)
	require.Empty(t, program.ParseError)

	assertHasDefinition(t, greeter, prefix+" Com/Example/Greeting/")
	assertHasDefinition(t, greeter, prefix+" Com/Example/Greeting/Greeter#")
	assertHasDefinition(t, greeter, prefix+" Com/Example/Greeting/Greeter#greet().")
	assertHasDefinition(t, greeter, prefix+" greeter/greeter/")
	assertHasDefinition(t, program, prefix+" Com/Example/App/Program#self.main().")

	// The cross-file claim, made three times over: the call in program.rb and the
	// method in greeter.rb render the same string; so do the constant path and the
	// module declaration; and so do the `require` and the load unit. Neither
	// file's extraction ever saw the other.
	assert.Equal(t, prefix+" Com/Example/Greeting/Greeter#greet().",
		referenceNamed(t, program, "greet").Descriptor.String())
	assert.Equal(t, prefix+" Com/Example/Greeting/",
		referenceNamed(t, program, "Greeting").Descriptor.String())
	assert.Equal(t, prefix+" greeter/greeter/",
		referenceNamed(t, program, "greeter").Descriptor.String())
}

// --- helpers ---------------------------------------------------------------

func assertHasDefinition(t *testing.T, ff facts.FileFacts, descriptor string) {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.Descriptor.String() == descriptor {
			return
		}
	}
	t.Fatalf("no definition with descriptor %q in %v", descriptor, definitionDescriptors(ff))
}

func referenceNamed(t *testing.T, ff facts.FileFacts, name string) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference && o.Name == name {
			return o
		}
	}
	t.Fatalf("no reference named %q", name)
	return facts.Occurrence{}
}

// unitDefinition returns the load-unit definition, which is the zero-width
// package occurrence at the start of the file.
func unitDefinition(t *testing.T, ff facts.FileFacts) facts.Occurrence {
	t.Helper()
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.SymbolKind == facts.KindPackage && o.RangeEnd == 0 {
			return o
		}
	}
	t.Fatal("no load-unit definition")
	return facts.Occurrence{}
}

// assertResolvesLocally checks that the reference named name carries descriptor
// and has a references_local edge to the definition with the same descriptor.
func assertResolvesLocally(t *testing.T, ff facts.FileFacts, name, descriptor string) {
	t.Helper()
	ref := referenceNamed(t, ff, name)
	assert.Equal(t, descriptor, ref.Descriptor.String())

	var def facts.LocalID
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition && o.Descriptor.String() == descriptor {
			def = o.ID
		}
	}
	require.NotEqual(t, facts.NoID, def, "no definition with descriptor %q", descriptor)

	for _, e := range ff.Edges {
		if e.Kind == facts.EdgeReferencesLocal && e.Source.ID == ref.ID && e.Target.ID == def {
			return
		}
	}
	t.Fatalf("no references_local edge from the reference to %q", descriptor)
}

func definitionsByName(ff facts.FileFacts) map[string]facts.Occurrence {
	out := map[string]facts.Occurrence{}
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition {
			out[o.Name] = o
		}
	}
	return out
}

func definitionDescriptors(ff facts.FileFacts) []string {
	out := []string{}
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleDefinition {
			out = append(out, o.Descriptor.String())
		}
	}
	sort.Strings(out)
	return out
}

func referenceDescriptors(ff facts.FileFacts) []string {
	out := []string{}
	for _, o := range ff.Occurrences {
		if o.Role == facts.RoleReference {
			out = append(out, o.Descriptor.String())
		}
	}
	sort.Strings(out)
	return out
}
