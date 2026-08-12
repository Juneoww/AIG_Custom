package reports

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedReportFontMatchesPinnedReviewedAsset(t *testing.T) {
	font, err := reportAssets.ReadFile("assets/DroidSansFallbackFull.ttf")
	require.NoError(t, err)
	assert.Len(t, font, 4_033_576)
	digest := sha256.Sum256(font)
	assert.Equal(t, "2392015530438bafc48edfc4aee6d9de2387f627a6134d8ab3dfcc99d21c8240", hex.EncodeToString(digest[:]))
}

func TestEmbeddedReportFontNoticesShipWithBinaryArtifacts(t *testing.T) {
	dockerfile := readPackagingFile(t, "../../../Dockerfile")
	for _, required := range []string{
		"COPY --from=builder /app/LICENSE /app/licenses/LICENSE",
		"COPY --from=builder /app/internal/platform/reports/assets/DROID_FONT_LICENSE.txt /app/licenses/DROID_FONT_LICENSE.txt",
		"COPY --from=builder /app/internal/platform/reports/assets/THIRD_PARTY_NOTICES.txt /app/licenses/THIRD_PARTY_NOTICES.txt",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("runtime image does not ship required license artifact %q", required)
		}
	}

	releaseWorkflow := readPackagingFile(t, "../../../.github/workflows/create-release.yml")
	for _, required := range []string{
		"mkdir -p release-package/licenses",
		"cp internal/platform/reports/assets/DROID_FONT_LICENSE.txt release-package/licenses/DROID_FONT_LICENSE.txt",
		"cp internal/platform/reports/assets/THIRD_PARTY_NOTICES.txt release-package/licenses/THIRD_PARTY_NOTICES.txt",
	} {
		if !strings.Contains(releaseWorkflow, required) {
			t.Errorf("release archive does not ship required attribution artifact %q", required)
		}
	}
}

func TestThirdPartyNoticesContainRequiredBinaryDistributionTerms(t *testing.T) {
	notices := readPackagingFile(t, "assets/THIRD_PARTY_NOTICES.txt")
	normalized := strings.Join(strings.Fields(notices), " ")
	for _, required := range []string{
		"Copyright (c) 2015 signintech",
		"Copyright (c) 2019-2020 David Barnes",
		"Copyright (c) 2017 Setasign - Jan Slabon",
		"Copyright (c) 2015, Dave Cheney",
		"Copyright 2009 The Go Authors",
		"ef0abb2c9d81c5ccf30590664fa08e2300df758790cebb62e5d74c45be4f32f0",
		"ba28af8fcfb6e83a8f1232bed9b22076c268e98551da03c0a8f146d5e3cf0698",
		"8d427fd87bc9579ea368fde3d49f9ca22eac857f91a9dec7e3004bdfab7dee86",
		"911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad",
		"The above copyright notice and this permission notice shall be included",
		"Redistributions in binary form must reproduce the above copyright notice",
		"Neither the name of Google LLC nor the names of its contributors",
		"THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS \"AS IS\"",
	} {
		assert.Contains(t, normalized, required)
	}
}

func TestPDFInteroperabilityWorkflowRequiresPopplerValidation(t *testing.T) {
	workflow := readPackagingFile(t, "../../../.github/workflows/report-pdf-qa.yml")
	for _, required := range []string{
		"poppler-utils",
		"AIG_REQUIRE_POPPLER: \"1\"",
		"TestPDFRendererProducesPopplerReadableMultiPageUnicodeDocument",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("PDF QA workflow does not enforce %q", required)
		}
	}
}

func readPackagingFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}
