package api

import (
	"net/http"
	"strings"

	"github.com/cpim-mes/backend/internal/domain"
	"github.com/go-playground/validator/v10"
)

// validate — 全 API ハンドラ共有のバリデータインスタンス
var validate = validator.New()

// validateBody — DTO に対して struct タグベースのバリデーションを実施し、
// 失敗時は 400 でレスポンスを返す。エラーがあれば true、なければ false。
func validateBody(w http.ResponseWriter, v any) bool {
	if err := validate.Struct(v); err != nil {
		writeError(w, http.StatusBadRequest, domain.NewValidation(formatValidationErrors(err)))
		return true
	}
	return false
}

// formatValidationErrors — go-playground/validator のエラーを人間可読な詳細にする
func formatValidationErrors(err error) []map[string]string {
	verrs, ok := err.(validator.ValidationErrors)
	if !ok {
		return []map[string]string{{"error": err.Error()}}
	}
	out := make([]map[string]string, 0, len(verrs))
	for _, fe := range verrs {
		out = append(out, map[string]string{
			"field":   strings.ToLower(fe.Field()[:1]) + fe.Field()[1:],
			"rule":    fe.Tag(),
			"param":   fe.Param(),
			"message": humanizeRule(fe),
		})
	}
	return out
}

func humanizeRule(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fe.Field() + "は必須です"
	case "min":
		return fe.Field() + "は最低 " + fe.Param() + " 以上必要です"
	case "max":
		return fe.Field() + "は最大 " + fe.Param() + " までです"
	case "gte":
		return fe.Field() + "は " + fe.Param() + " 以上必要です"
	case "lte":
		return fe.Field() + "は " + fe.Param() + " 以下である必要があります"
	case "gt":
		return fe.Field() + "は " + fe.Param() + " より大きい必要があります"
	case "lt":
		return fe.Field() + "は " + fe.Param() + " より小さい必要があります"
	case "oneof":
		return fe.Field() + "は次のいずれかでなければなりません: " + fe.Param()
	case "email":
		return fe.Field() + "は有効なメールアドレスである必要があります"
	default:
		return fe.Field() + " は不正です (" + fe.Tag() + ")"
	}
}
