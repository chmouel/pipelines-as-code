package annotations

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestGetAnnotationValues(t *testing.T) {
	type args struct {
		annotation string
	}
	tests := []struct {
		name    string
		args    args
		want    []string
		wantErr bool
	}{
		{
			name: "get-annotation-string",
			args: args{
				annotation: "foo",
			},
			want:    []string{"foo"},
			wantErr: false,
		},
		{
			name: "get-annotation-simple",
			args: args{
				annotation: "[foo]",
			},
			want:    []string{"foo"},
			wantErr: false,
		},
		{
			name: "get-annotation-multiples",
			args: args{
				annotation: "[foo, bar]",
			},
			want:    []string{"foo", "bar"},
			wantErr: false,
		},
		{
			name: "get-annotation-multiple-string-bad-syntax",
			args: args{
				annotation: "foo, bar",
			},
			wantErr: true,
		},
		{
			name: "get-annotation-bad-syntax",
			args: args{
				annotation: "foo]",
			},
			wantErr: true,
		},
		{
			name: "get-annotation-error-empty",
			args: args{
				annotation: "[]",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetAnnotationValues(tt.args.annotation)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetAnnotationValues() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.DeepEqual(t, got, tt.want)
		})
	}
}
