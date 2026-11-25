// Copyright 2017 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cc

import (
	"android/soong/cc/config"
	"fmt"
	"strings"

	"github.com/google/blueprint"
	"github.com/google/blueprint/proptools"

	"android/soong/android"
)

// LTO (link-time optimization) allows the compiler to optimize and generate
// code for the entire module at link time, rather than per-compilation
// unit. LTO is required for Clang CFI and other whole-program optimization
// techniques. LTO also allows cross-compilation unit optimizations that should
// result in faster and smaller code, at the expense of additional compilation
// time.
//
// To properly build a module with LTO, the module and all recursive static
// dependencies should be compiled with -flto which directs the compiler to emit
// bitcode rather than native object files. These bitcode files are then passed
// by the linker to the LLVM plugin for compilation at link time. Static
// dependencies not built as bitcode will still function correctly but cannot be
// optimized at link time and may not be compatible with features that require
// LTO, such as CFI.
//
// This file adds support to soong to automatically propagate LTO options to a
// new variant of all static dependencies for each module with LTO enabled.

type LTOProperties struct {
	// Lto must violate capitalization style for acronyms so that it can be
	// referred to in blueprint files as "lto"
	Lto struct {
		Never *bool `android:"arch_variant"`
		Thin  *bool `android:"arch_variant"`
	} `android:"arch_variant"`

	LtoEnabled bool `blueprint:"mutated"`
	LtoDefault bool `blueprint:"mutated"`

	// Use -fwhole-program-vtables cflag.
	Whole_program_vtables *bool

	// Use --lto-O0 flag.
	Lto_O0 *bool
}

type lto struct {
	Properties LTOProperties
}

func (lto *lto) props() []interface{} {
	return []interface{}{&lto.Properties}
}

// FullLTO reports whether LTO-enabled modules are built with monolithic ("full")
// LTO rather than the AOSP default of ThinLTO. Full LTO gives the optimizer the
// whole library at once instead of importing a bounded number of instructions
// per call site, at the cost of a serial, memory-hungry link step.
//
// Opt-in per build with YASVARA_FULL_LTO=true (build.sh sets it for release
// devices), so quick test-device builds keep ThinLTO. Other Soong plugins
// (notably art/build/art.go, which sizes its -Wframe-larger-than budget to
// match) call this so the whole tree agrees on which mode is in effect.
func FullLTO(config android.Config) bool {
	return config.IsEnvTrue("YASVARA_FULL_LTO")
}

func (lto *lto) begin(ctx BaseModuleContext) {
	// First, determine the module independent default LTO mode.
	ltoDefault := true
	if ctx.Config().IsEnvTrue("DISABLE_LTO") {
		ltoDefault = false
	} else if lto.Never() {
		// TODO(b/308229434) investigate root cause
		// ltoDefault = false
	} else if ctx.Host() {
		// Performance and binary size are less important for host binaries.
		ltoDefault = false
	} else if ctx.Arch().ArchType.Multilib == "lib32" {
		// LP32 has many subtle issues and less test coverage.
		ltoDefault = false
	}

	// Then, determine the actual LTO mode to use. If different from `ltoDefault`, a variant needs
	// to be created.
	ltoEnabled := ltoDefault
	if lto.Never() {
		ltoEnabled = false
	} else if lto.ThinLTO() {
		// Module explicitly requests for LTO.
		ltoEnabled = true
	} else if ctx.testBinary() || ctx.testLibrary() {
		// Do not enable LTO for tests for better debugging.
		ltoEnabled = false
	}

	lto.Properties.LtoDefault = ltoDefault
	lto.Properties.LtoEnabled = ltoEnabled
}

func (lto *lto) flags(ctx ModuleContext, flags Flags) Flags {
	// TODO(b/131771163): CFI and Fuzzer controls LTO flags by themselves.
	// This has be checked late because these properties can be mutated.
	if ctx.isCfi() || ctx.isFuzzer() {
		return flags
	}
	if lto.Properties.LtoEnabled {
		fullLTO := FullLTO(ctx.Config())
		ltoMode := "-flto=thin"
		if fullLTO {
			ltoMode = "-flto=full"
		}
		ltoCFlags := []string{ltoMode, "-fsplit-lto-unit"}
		if lto.ThinLTO() && flags.Sdclang && !strings.Contains(config.SDClangPath, "9.0") {
			// TODO(b/129607781) sdclang does not currently support
			// the "-fsplit-lto-unit" option
			ltoCFlags = []string{ltoMode}
		}

		var ltoLdFlags []string

		// Do not perform costly LTO optimizations for Eng builds.
		if Bool(lto.Properties.Lto_O0) || ctx.optimizeForSize() || ctx.Config().Eng() {
			ltoLdFlags = append(ltoLdFlags, "-Wl,--lto-O0")
		} else if ltoOLevel := ctx.Config().Getenv("LTO_O_LEVEL"); ltoOLevel != "" {
			ltoLdFlags = append(ltoLdFlags, "-Wl,--lto-O"+ltoOLevel)
		} else if fullLTO {
			// lld defaults --lto-O to 2. Under full LTO that is the level that
			// matters: the per-TU -O only drives the pre-link pipeline, while
			// every whole-program decision -- inlining across the entire
			// library, the one thing full LTO exists for -- happens in the
			// link-time codegen governed by this flag. Thousands of TUs compile
			// at -O3 and would otherwise get their real codegen at O2.
			//
			// Failure mode is loud: more inlining means larger frames, so an
			// overshoot trips ART's -Wframe-larger-than at build time rather
			// than misbehaving on the device. LTO_O_LEVEL=2 restores lld's
			// default.
			ltoLdFlags = append(ltoLdFlags, "-Wl,--lto-O3")
		}

		// Opt-in only. Do NOT enable this by default for full-LTO modules: it
		// miscompiles ART. Within libart.so's LTO unit,
		// ClassLinker::DenyAccessBasedOnPublicSdk looks like the only
		// implementer of that virtual, so LLVM devirtualizes callers to its
		// LOG(FATAL) body -- the real override lives in dex2oat's
		// AotClassLinker -- and every dex2oat job hitting a hidden-API check
		// SIGABRTs. Any shared library subclassed from another binary is
		// exposed to this, so it stays a per-module decision.
		if Bool(lto.Properties.Whole_program_vtables) {
			ltoCFlags = append(ltoCFlags, "-fwhole-program-vtables")
		}

		// The incremental cache is keyed off the per-module summary index, which
		// only ThinLTO produces.
		if !fullLTO && ctx.Config().IsEnvTrue("USE_THINLTO_CACHE") {
			// Set appropriate ThinLTO cache policy
			cacheDirFormat := "-Wl,--thinlto-cache-dir="
			cacheDir := android.PathForOutput(ctx, "thinlto-cache").String()
			ltoLdFlags = append(ltoLdFlags, cacheDirFormat+cacheDir)

			// Limit the size of the ThinLTO cache to the lesser of 10% of available
			// disk space and 10GB.
			cachePolicyFormat := "-Wl,--thinlto-cache-policy="
			policy := "cache_size=10%:cache_size_bytes=10g"
			ltoLdFlags = append(ltoLdFlags, cachePolicyFormat+policy)
		}

		// Reduce the inlining threshold for a better balance of binary size and
		// performance. This bounds ThinLTO's cross-module import step; full LTO
		// holds the whole library in one module, so there is nothing to import.
		if !ctx.Darwin() && !fullLTO {
			if ctx.isAfdoCompile(ctx) {
				ltoLdFlags = append(ltoLdFlags, "-Wl,-plugin-opt,-import-instr-limit=40")
			} else {
				ltoLdFlags = append(ltoLdFlags, "-Wl,-plugin-opt,-import-instr-limit=5")
			}
		}

		if !ctx.Config().IsEnvFalse("THINLTO_USE_MLGO") {
			// Register allocation MLGO flags for ARM64.
			// Combining with LFI fails with:
			// ld.lld: error: Requested regalloc eviction advisor analysis could not be created. Using default
			if ctx.Arch().ArchType == android.Arm64 {
				// TODO: b/311889575 - 15701243645303822394 - Build Failure for libperfetto.so
				// ltoCFlags = append(ltoCFlags, "-mllvm -regalloc-enable-advisor=release")
				// ltoLdFlags = append(ltoLdFlags, "-Wl,-mllvm,-regalloc-enable-advisor=release")
			}
			// Flags for training MLGO model.
			if !fullLTO && ctx.Config().IsEnvTrue("THINLTO_EMIT_INDEXES_AND_IMPORTS") {
				ltoLdFlags = append(ltoLdFlags, "-Wl,--save-temps=import")
				ltoLdFlags = append(ltoLdFlags, "-Wl,--thinlto-emit-index-files")
			}
		}

		flags.Local.CFlags = append(flags.Local.CFlags, ltoCFlags...)
		flags.Local.AsFlags = append(flags.Local.AsFlags, ltoCFlags...)
		flags.Local.LdFlags = append(flags.Local.LdFlags, ltoCFlags...)
		flags.Local.LdFlags = append(flags.Local.LdFlags, ltoLdFlags...)
	}
	return flags
}

func (lto *lto) ThinLTO() bool {
	return lto != nil && proptools.Bool(lto.Properties.Lto.Thin)
}

func (lto *lto) Never() bool {
	return lto != nil && proptools.Bool(lto.Properties.Lto.Never)
}

func ltoPropagateViaDepTag(tag blueprint.DependencyTag) bool {
	libTag, isLibTag := tag.(libraryDependencyTag)
	// Do not recurse down non-static dependencies
	if isLibTag {
		return libTag.static()
	} else {
		return tag == objDepTag || tag == reuseObjTag || tag == staticVariantTag
	}
}

// ltoTransitionMutator creates LTO variants of cc modules.  Variant "" is the default variant, which may
// or may not have LTO enabled depending on the config and the module's type and properties.  "lto-thin" or
// "lto-none" variants are created when a module needs to compile in the non-default state for that module.
type ltoTransitionMutator struct{}

const LTO_NONE_VARIATION = "lto-none"
const LTO_THIN_VARIATION = "lto-thin"

func (l *ltoTransitionMutator) Split(ctx android.BaseModuleContext) []string {
	return []string{""}
}

func (l *ltoTransitionMutator) SplitOnDemand(ctx android.BaseModuleContext) []string {
	return nil
}

func (l *ltoTransitionMutator) OutgoingTransition(ctx android.OutgoingTransitionContext, sourceVariation string) string {
	if m, ok := ctx.Module().(*Module); ok && m.lto != nil {
		if !ltoPropagateViaDepTag(ctx.DepTag()) {
			return ""
		}

		if sourceVariation != "" {
			return sourceVariation
		}

		// Always request an explicit variation, IncomingTransition will rewrite it back to the default variation
		// if necessary.
		if m.lto.Properties.LtoEnabled {
			return LTO_THIN_VARIATION
		} else {
			return LTO_NONE_VARIATION
		}
	}
	return ""
}

func (l *ltoTransitionMutator) IncomingTransition(ctx android.IncomingTransitionContext, incomingVariation string) string {
	if m, ok := ctx.Module().(*Module); ok && m.lto != nil {
		if m.lto.Never() {
			return ""
		}
		if ctx.Target().LFI && m.staticLibrary() {
			return ""
		}
		// Rewrite explicit variations back to the default variation if the default variation matches.
		if incomingVariation == LTO_THIN_VARIATION && m.lto.Properties.LtoDefault {
			return ""
		} else if incomingVariation == LTO_NONE_VARIATION && !m.lto.Properties.LtoDefault {
			return ""
		}
		return incomingVariation
	}
	return ""
}

func (l *ltoTransitionMutator) Mutate(ctx android.BottomUpMutatorContext, variation string) {
	// Default module which will be installed. Variation set above according to explicit LTO properties.
	if variation == "" {
		return
	}

	if m, ok := ctx.Module().(*Module); ok && m.lto != nil {
		// Non-default variation, set the LTO properties to match the variation.
		switch variation {
		case LTO_THIN_VARIATION:
			m.lto.Properties.LtoEnabled = true
		case LTO_NONE_VARIATION:
			m.lto.Properties.LtoEnabled = false
		default:
			panic(fmt.Errorf("unknown variation %s", variation))
		}
		// Non-default variations are never installed.
		m.Properties.PreventInstall = true
		m.Properties.HideFromMake = true
	}
}
