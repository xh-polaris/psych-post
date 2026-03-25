package enum

// AlarmStatus
const (
	AlarmStatusProcessed = 1
	AlarmStatusPending   = 2
)

// AlarmEmotion 应按照严重程度升序
const (
	UnknownEmotion = iota
	AlarmEmotionDanger
	AlarmEmotionDepress
	AlarmEmotionAnxiety
	AlarmEmotionNegative
	AlarmEmotionNormal
)

const (
	Danger   = "danger"
	Depress  = "depress"
	Anxiety  = "anxiety"
	Negative = "negative"
	Normal   = "normal"
)

func EmotionS2i(emotion string) int {
	switch emotion {
	case Danger:
		return AlarmEmotionDanger
	case Depress:
		return AlarmEmotionDepress
	case Anxiety:
		return AlarmEmotionAnxiety
	case Normal:
		return AlarmEmotionNormal
	default:
		return UnknownEmotion
	}
}
