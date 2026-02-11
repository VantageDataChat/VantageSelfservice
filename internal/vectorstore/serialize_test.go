package vectorstore

import (
	"math"
	"testing"
)

func TestSerializeDeserializeRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		vec  []float64
	}{
		{"empty vector", []float64{}},
		{"single element", []float64{3.14}},
		{"multiple elements", []float64{1.0, 2.0, 3.0, 4.0, 5.0}},
		{"negative values", []float64{-1.5, -2.5, 0.0, 2.5, 1.5}},
		{"very small values", []float64{1e-300, -1e-300}},
		{"very large values", []float64{1e300, -1e300}},
		{"special values", []float64{0.0, math.Inf(1), math.Inf(-1)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := SerializeVector(tt.vec)
			got := DeserializeVector(data)

			if len(got) != len(tt.vec) {
				t.Fatalf("length mismatch: got %d, want %d", len(got), len(tt.vec))
			}
			for i := range tt.vec {
				if math.IsNaN(tt.vec[i]) {
					if !math.IsNaN(got[i]) {
						t.Errorf("index %d: expected NaN, got %v", i, got[i])
					}
				} else if got[i] != tt.vec[i] {
					t.Errorf("index %d: got %v, want %v", i, got[i], tt.vec[i])
				}
			}
		})
	}
}

func TestSerializeVectorByteLength(t *testing.T) {
	vec := []float64{1.0, 2.0, 3.0}
	data := SerializeVector(vec)
	expected := len(vec) * 8
	if len(data) != expected {
		t.Errorf("byte length: got %d, want %d", len(data), expected)
	}
}

func TestDeserializeEmptyData(t *testing.T) {
	got := DeserializeVector([]byte{})
	if len(got) != 0 {
		t.Errorf("expected empty slice, got length %d", len(got))
	}
}

func TestCosineSimilarityIdenticalVectors(t *testing.T) {
	a := []float64{1.0, 2.0, 3.0}
	sim := CosineSimilarity(a, a)
	if math.Abs(sim-1.0) > 1e-10 {
		t.Errorf("identical vectors should have similarity 1.0, got %v", sim)
	}
}

func TestCosineSimilarityOrthogonalVectors(t *testing.T) {
	a := []float64{1.0, 0.0}
	b := []float64{0.0, 1.0}
	sim := CosineSimilarity(a, b)
	if math.Abs(sim) > 1e-10 {
		t.Errorf("orthogonal vectors should have similarity 0.0, got %v", sim)
	}
}

func TestCosineSimilarityOppositeVectors(t *testing.T) {
	a := []float64{1.0, 2.0, 3.0}
	b := []float64{-1.0, -2.0, -3.0}
	sim := CosineSimilarity(a, b)
	if math.Abs(sim-(-1.0)) > 1e-10 {
		t.Errorf("opposite vectors should have similarity -1.0, got %v", sim)
	}
}

func TestCosineSimilarityZeroVector(t *testing.T) {
	a := []float64{0.0, 0.0, 0.0}
	b := []float64{1.0, 2.0, 3.0}

	if sim := CosineSimilarity(a, b); sim != 0 {
		t.Errorf("zero vector a: expected 0, got %v", sim)
	}
	if sim := CosineSimilarity(b, a); sim != 0 {
		t.Errorf("zero vector b: expected 0, got %v", sim)
	}
	if sim := CosineSimilarity(a, a); sim != 0 {
		t.Errorf("both zero: expected 0, got %v", sim)
	}
}

func TestCosineSimilarityScaledVectors(t *testing.T) {
	a := []float64{1.0, 2.0, 3.0}
	b := []float64{2.0, 4.0, 6.0} // 2x scaled
	sim := CosineSimilarity(a, b)
	if math.Abs(sim-1.0) > 1e-10 {
		t.Errorf("scaled vectors should have similarity 1.0, got %v", sim)
	}
}
