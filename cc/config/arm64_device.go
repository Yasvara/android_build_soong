// Copyright 2015 Google Inc. All rights reserved.
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

package config

import (
	"fmt"
	"strings"

	"android/soong/android"
)

var (
	arm64Cflags = []string{
		// Help catch common 32/64-bit errors.
		// Common to all LP64 architectures.
		"-Werror=implicit-function-declaration",

		// For stack allocations larger than a page, touch each page immediately
		// to ensure we hit the guard page on stack overflow.
		// Common to all LP64 architectures.
		"-fstack-clash-protection",
	}

	arm64ArchVariantCflags = map[string][]string{
		"armv8-a":            {"-march=armv8-a"},
		"armv8-a-branchprot": {"-march=armv8-a"},
		"armv8-2a":           {"-march=armv8.2-a"},
		"armv8-2a-dotprod":   {"-march=armv8.2-a+dotprod"},
		"armv8-5a":           {"-march=armv8.5-a"},
		"armv8-7a":           {"-march=armv8.7-a"},
		// +nosve is required, not a tuning choice: armv9-a implies SVE2, but the
		// kernels on our armv9 devices (sm8735 onyx) do not advertise SVE in
		// HWCAP, so any SVE instruction the vectorizer emits traps (SIGILL).
		//
		// +crypto turns on the AES/SHA intrinsic paths in third-party code
		// (e.g. abseil's randen_hwaes). Their "#pragma unroll" loops are not
		// always unrollable at -O2, and -Wpass-failed then fails -Werror
		// modules such as cronet. A missed unroll is only a missed hint, so
		// keep the diagnostic as a warning.
		"armv9-a": {
			"-march=armv9-a+crypto+nosve+dotprod+fp16+i8mm",
			"-Wno-error=pass-failed",
		},
		"armv9-2a": {"-march=armv9.2-a"},
		"armv9-3a": {"-march=armv9.3-a"},
		"armv9-4a": {"-march=armv9.4-a"},
	}

	arm64ArchFeatureCflags = map[string][]string{
		// When Pointer Authentication Codes (PAC) are available, -fstack-protector is unnecessary.
		"branchprot": {
			"-mbranch-protection=standard",
			"-fno-stack-protector",
		},
	}

	arm64Ldflags = []string{
		// Separate-code is required for XOM
		"-Wl,-z,separate-code",
		"-Wl,-z,separate-loadable-segments",
	}

	arm64ArchFeatureLdflags = map[string][]string{
		"branchprot": {
			"-Wl,-z,bti-report=warning",
		},
	}

	arm64Cppflags = []string{}

	arm64CpuVariantCflags = map[string][]string{
		// Cortex-A720 implements SVE2, so plain -mcpu=cortex-a720 would turn
		// on +sve +sve2 again; keep it off for the same reason as armv9-a.
		//
		// The rest is onyx's speed-over-hardening policy. These are ToolchainCflags, emitted after
		// the common/device globals (-O2, stack protector, zero-init), so they win; module-local
		// flags still win over them. PAC/BTI is dropped in arm64Features. Frame pointers are not
		// needed: unwinding uses .eh_frame, profiling uses ETM.
		// Calls to a library's own exported functions bind locally (no LD_PRELOAD interposition).
		// No _FORTIFY_SOURCE; bionic declares its exported __*_chk helpers through
		// __BIONIC_DECLARE_FORTIFY_HELPERS instead.
		// Zero-init of stack variables stays: AOSP has uninitialized members it hides (ART's
		// InlinedVector::size_ crashed zygote in preload without it). It costs well under 1%.
		"cortex-a720": []string{
			"-mcpu=cortex-a720+nosve",
			"-O3",
			"-fno-stack-protector",
			"-fno-stack-clash-protection",
			"-fomit-frame-pointer",
			"-U_FORTIFY_SOURCE",
			"-fno-semantic-interposition",
		},
		"cortex-a53": []string{
			"-mcpu=cortex-a53",
		},
		"cortex-a55": []string{
			"-mcpu=cortex-a55",
		},
		"cortex-a75": []string{
			// Use the cortex-a55 since it is similar to the little
			// core (cortex-a55) and is sensitive to ordering.
			"-mcpu=cortex-a55",
		},
		"cortex-a76": []string{
			// Use the cortex-a55 since it is similar to the little
			// core (cortex-a55) and is sensitive to ordering.
			"-mcpu=cortex-a55",
		},
		"kryo": []string{
			"-mcpu=kryo",
		},
		"kryo385": []string{
			// Use cortex-a53 because kryo385 is not supported in clang.
			"-mcpu=cortex-a53",
		},
		"exynos-m1": []string{
			"-mcpu=exynos-m1",
		},
		"exynos-m2": []string{
			"-mcpu=exynos-m2",
		},
	}
)

func init() {
	pctx.VariableFunc("Arm64Ldflags", func(ctx android.PackageVarContext) string {
		maxPageSizeFlag := "-Wl,-z,max-page-size=" + ctx.Config().MaxPageSizeSupported()
		flags := append(arm64Ldflags, maxPageSizeFlag)
		return strings.Join(flags, " ")
	})

	pctx.VariableFunc("Arm64Cflags", func(ctx android.PackageVarContext) string {
		flags := arm64Cflags
		if ctx.Config().NoBionicPageSizeMacro() {
			flags = append(flags, "-D__BIONIC_NO_PAGE_SIZE_MACRO")
		} else {
			flags = append(flags, "-D__BIONIC_DEPRECATED_PAGE_SIZE_MACRO")
		}
		return strings.Join(flags, " ")
	})

	pctx.StaticVariable("Arm64Cppflags", strings.Join(arm64Cppflags, " "))

	for variant, cflags := range arm64ArchVariantCflags {
		pctx.StaticVariable("Arm64"+variant+"VariantCflags", strings.Join(cflags, " "))
	}

	pctx.StaticVariable("Arm64CortexA53Cflags", strings.Join(arm64CpuVariantCflags["cortex-a53"], " "))
	pctx.StaticVariable("Arm64CortexA55Cflags", strings.Join(arm64CpuVariantCflags["cortex-a55"], " "))
	pctx.StaticVariable("Arm64CortexA720Cflags", strings.Join(arm64CpuVariantCflags["cortex-a720"], " "))
	pctx.StaticVariable("Arm64KryoCflags", strings.Join(arm64CpuVariantCflags["kryo"], " "))
	pctx.StaticVariable("Arm64ExynosM1Cflags", strings.Join(arm64CpuVariantCflags["exynos-m1"], " "))
	pctx.StaticVariable("Arm64ExynosM2Cflags", strings.Join(arm64CpuVariantCflags["exynos-m2"], " "))

	pctx.StaticVariable("Arm64FixCortexA53Ldflags", "-Wl,--fix-cortex-a53-843419")
}

var (
	arm64CpuVariantCflagsVar = map[string]string{
		"cortex-a53":  "${config.Arm64CortexA53Cflags}",
		"cortex-a55":  "${config.Arm64CortexA55Cflags}",
		"cortex-a72":  "${config.Arm64CortexA53Cflags}",
		"cortex-a73":  "${config.Arm64CortexA53Cflags}",
		"cortex-a75":  "${config.Arm64CortexA55Cflags}",
		"cortex-a76":  "${config.Arm64CortexA55Cflags}",
		"cortex-a720": "${config.Arm64CortexA720Cflags}",
		"kryo":        "${config.Arm64KryoCflags}",
		"kryo385":     "${config.Arm64CortexA53Cflags}",
		"exynos-m1":   "${config.Arm64ExynosM1Cflags}",
		"exynos-m2":   "${config.Arm64ExynosM2Cflags}",
	}

	arm64CpuVariantLdflags = map[string]string{
		"cortex-a53": "${config.Arm64FixCortexA53Ldflags}",
		"cortex-a72": "${config.Arm64FixCortexA53Ldflags}",
		"cortex-a73": "${config.Arm64FixCortexA53Ldflags}",
		"kryo":       "${config.Arm64FixCortexA53Ldflags}",
		"exynos-m1":  "${config.Arm64FixCortexA53Ldflags}",
		"exynos-m2":  "${config.Arm64FixCortexA53Ldflags}",
	}
)

type toolchainArm64 struct {
	toolchainBionic
	toolchain64Bit

	toolchainLdflags string
	toolchainCflags  string
}

func (t *toolchainArm64) Name() string {
	return "arm64"
}

func (t *toolchainArm64) IncludeFlags() string {
	return ""
}

func (t *toolchainArm64) ClangTriple() string {
	return "aarch64-linux-android"
}

func (t *toolchainArm64) Cflags() string {
	return "${config.Arm64Cflags}"
}

func (t *toolchainArm64) Cppflags() string {
	return "${config.Arm64Cppflags}"
}

func (t *toolchainArm64) Ldflags(ctx ToolchainFlagsContext) FlagsWithDeps {
	return FlagsWithDeps{
		Flags: "${config.Arm64Ldflags}",
	}
}

func (t *toolchainArm64) ToolchainCflags() string {
	return t.toolchainCflags
}

func (t *toolchainArm64) ToolchainLdflags() FlagsWithDeps {
	return FlagsWithDeps{
		Flags: t.toolchainLdflags,
	}
}

func (toolchainArm64) LibclangRuntimeLibraryArch() string {
	return "aarch64"
}

func arm64ToolchainFactory(arch android.Arch) Toolchain {
	toolchainCflags, toolchainLdflags := arm64ToolchainFlags(arch)
	return &toolchainArm64{
		toolchainCflags:  toolchainCflags,
		toolchainLdflags: toolchainLdflags,
	}
}

func arm64ToolchainFlags(arch android.Arch) (string, string) {
	// Error now rather than having a confusing Ninja error
	if _, ok := arm64ArchVariantCflags[arch.ArchVariant]; !ok {
		panic(fmt.Sprintf("Unknown ARM64 architecture version: %q", arch.ArchVariant))
	}

	toolchainCflags := []string{"${config.Arm64" + arch.ArchVariant + "VariantCflags}"}
	toolchainCflags = append(toolchainCflags,
		variantOrDefault(arm64CpuVariantCflagsVar, arch.CpuVariant))
	for _, feature := range arm64Features(arch) {
		toolchainCflags = append(toolchainCflags, arm64ArchFeatureCflags[feature]...)
	}

	extraLdflags := []string{"${config.Arm64Ldflags}"}
	extraLdflags = append(extraLdflags,
		variantOrDefault(arm64CpuVariantLdflags, arch.CpuVariant))
	for _, feature := range arm64Features(arch) {
		extraLdflags = append(extraLdflags, arm64ArchFeatureLdflags[feature]...)
	}
	return strings.Join(toolchainCflags, " "), strings.Join(extraLdflags, " ")
}

// arm64Features drops branchprot (PAC/BTI) for cortex-a720 (onyx), whose speed-over-hardening
// policy builds without it. Dropping the feature, not overriding its flags, also removes
// -z bti-report, which --fatal-warnings turns into an error once objects lack BTI notes.
func arm64Features(arch android.Arch) []string {
	if arch.CpuVariant != "cortex-a720" {
		return arch.ArchFeatures
	}
	var features []string
	for _, f := range arch.ArchFeatures {
		if f != "branchprot" {
			features = append(features, f)
		}
	}
	return features
}

func init() {
	registerToolchainFactory(android.Android, android.Arm64, arm64ToolchainFactory)
}
