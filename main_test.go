package main

import (
	"os"
	"path/filepath"
	"testing"
)

func Test_exportOutput(t *testing.T) {
	tests := []struct {
		name      string
		artifacts []string
		want      string
		wantErr   bool
	}{
		{
			name:      "One arifact",
			artifacts: []string{"First.app"},
			want: func() string {
				dir := os.Getenv("BITRISE_DEPLOY_DIR")
				pth := filepath.Join(dir, "First.app")
				return pth
			}(),
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := exportOutput(tt.artifacts)
			if (err != nil) != tt.wantErr {
				t.Errorf("exportOutput() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("exportOutput() = %v, want %v", got, tt.want)
			}
		})
	}
}
