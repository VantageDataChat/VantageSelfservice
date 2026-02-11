package vectorstore

import (
	"bytes"
	"encoding/binary"
	"math"
)

// SerializeVector converts a float64 slice to a byte slice using little-endian encoding.
// Each float64 occupies 8 bytes in the output.
func SerializeVector(vec []float64) []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, vec)
	return buf.Bytes()
}

// DeserializeVector converts a byte slice back to a float64 slice using little-endian decoding.
// The input length must be a multiple of 8 bytes.
func DeserializeVector(data []byte) []float64 {
	vec := make([]float64, len(data)/8)
	buf := bytes.NewReader(data)
	binary.Read(buf, binary.LittleEndian, &vec)
	return vec
}

// CosineSimilarity computes the cosine similarity between two float64 vectors.
// Returns 0 if either vector has zero magnitude.
func CosineSimilarity(a, b []float64) float64 {
	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}
