package main

import (
	"errors"
	"testing"

	"github.com/joomcode/errorx"
)

func Test_parseError(t *testing.T) {
	type args struct {
		_err error
	}
	tests := []struct {
		name     string
		args     args
		wantErr  bool
		wantType *errorx.Type
	}{
		{name: "RateLimited", args: args{_err: errors.New("[GET /fake/path](429)")}, wantErr: true, wantType: RateLimited},
		{name: "NotFound", args: args{_err: errors.New("[GET /fake/path](404)")}, wantErr: true, wantType: NotFound},
		{name: "ClientTimeout", args: args{_err: errors.New("Client.Timeout response cancelled")}, wantErr: true, wantType: Timeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := parseError(tt.args._err); (err != nil) != tt.wantErr && errorx.IsOfType(err, tt.wantType) {
				t.Errorf("parseError() error = %v, wantErr %v", err, tt.wantErr)
			}

		})
	}
}
