package enum

// UserGender
const (
	UserGenderMale   = 1
	UserGenderFemale = 2
	UserGenderOther  = 3
)

// UserCodeType
const (
	UserCodeTypePhone     = 1
	UserCodeTypeStudentID = 2
)

// UserRole
const (
	UserRoleStudent      = 1
	UserRoleTeacher      = 2
	UserRoleClassTeacher = 3
	UserRoleUnitAdmin    = 4
	UserRoleSuperAdmin   = 5
)

// UserRiskLevel 与报告 simple_report.riskLevel 对齐，数值越大风险越高。
const (
	UserRiskLevelUnknown    = -1
	UserRiskLevelLow        = 0
	UserRiskLevelMediumLow  = 1
	UserRiskLevelMediumHigh = 2
	UserRiskLevelHigh       = 3
)

// UserStatus
const (
	UserStatusActive  = 1
	UserStatusDeleted = 2
)

var GenderI2S = map[int]string{
	UserGenderMale:   "男",
	UserGenderFemale: "女",
	UserGenderOther:  "未知",
}
