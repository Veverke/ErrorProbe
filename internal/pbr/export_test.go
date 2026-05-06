// export_test.go exposes internal symbols for white-box testing.
package pbr

// ParseConditionForTest wraps parseCondition for use in tests.
var ParseConditionForTest = parseCondition

// FieldValueForTest wraps fieldValue for use in tests.
var FieldValueForTest = fieldValue

// ValidateUniquePriorityForTest wraps validateUniquePriority for tests.
var ValidateUniquePriorityForTest = validateUniquePriority
