package rules

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

//go:embed rule.schema.json
var ruleSchemaJSON []byte

// ValidationError represents a structured validation error
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ValidationResult contains validation results
type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors,omitempty"`
}

// ValidateRuleJSON validates a rule from JSON bytes and returns detailed errors
func ValidateRuleJSON(jsonData []byte) *ValidationResult {
	result := &ValidationResult{Valid: true, Errors: []ValidationError{}}

	// Try to parse JSON
	var rule RuleConditions
	if err := json.Unmarshal(jsonData, &rule); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "json",
			Message: fmt.Sprintf("Invalid JSON: %s", err.Error()),
			Code:    "INVALID_JSON",
		})
		return result
	}

	// Validate the rule
	if err := ValidateRuleConditions(&rule); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "rule",
			Message: err.Error(),
			Code:    "VALIDATION_ERROR",
		})
	}

	return result
}

// ValidateRuleJSONDetailed validates and provides detailed field-level errors
func ValidateRuleJSONDetailed(jsonData []byte) *ValidationResult {
	result := &ValidationResult{Valid: true, Errors: []ValidationError{}}

	// Try to parse JSON
	var rule RuleConditions
	if err := json.Unmarshal(jsonData, &rule); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "json",
			Message: fmt.Sprintf("Invalid JSON: %s", err.Error()),
			Code:    "INVALID_JSON",
		})
		return result
	}

	// Validate logic
	if rule.Logic == "" {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "logic",
			Message: "Logic is required",
			Code:    "REQUIRED_FIELD",
		})
	} else {
		logic := LogicOperator(strings.ToUpper(rule.Logic))
		if logic != LogicAND && logic != LogicOR {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   "logic",
				Message: fmt.Sprintf("Logic must be 'AND' or 'OR', got: %s", rule.Logic),
				Code:    "INVALID_VALUE",
			})
		}
	}

	// Validate conditions
	if len(rule.Conditions) == 0 {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "conditions",
			Message: "At least one condition is required",
			Code:    "REQUIRED_FIELD",
		})
	} else {
		for i, condition := range rule.Conditions {
			fieldPrefix := fmt.Sprintf("conditions[%d]", i)
			validateConditionDetailed(&condition, fieldPrefix, result)
		}
	}

	return result
}

func validateConditionDetailed(condition *Condition, fieldPrefix string, result *ValidationResult) {
	if !validateBasicFields(condition, fieldPrefix, result) {
		return
	}

	field := FieldType(condition.Field)
	operator := OperatorType(condition.Operator)

	if !validateFieldOperatorMatch(field, operator, condition, fieldPrefix, result) {
		return
	}

	validateValueRequirements(operator, condition, fieldPrefix, result)
	validateFieldSpecificRules(field, operator, condition, fieldPrefix, result)
}

func validateBasicFields(condition *Condition, fieldPrefix string, result *ValidationResult) bool {
	if condition.Field == "" {
		addError(result, fieldPrefix+".field", "Field is required", "REQUIRED_FIELD")
		return false
	}

	field := FieldType(condition.Field)
	isValidField := IsStringField(field) || IsNumericField(field)
	if !isValidField {
		addError(result, fieldPrefix+".field", fmt.Sprintf("Invalid field: %s", condition.Field), "INVALID_FIELD")
		return false
	}

	if condition.Operator == "" {
		addError(result, fieldPrefix+".operator", "Operator is required", "REQUIRED_FIELD")
		return false
	}

	return true
}

func validateFieldOperatorMatch(field FieldType, operator OperatorType, condition *Condition, fieldPrefix string, result *ValidationResult) bool {
	isStringFieldWithBadOperator := IsStringField(field) && !IsStringOperator(operator)
	isNumericFieldWithBadOperator := IsNumericField(field) && !IsNumericOperator(operator)

	if isStringFieldWithBadOperator {
		msg := fmt.Sprintf("Operator '%s' is not valid for string field '%s'", condition.Operator, condition.Field)
		addError(result, fieldPrefix+".operator", msg, "INVALID_OPERATOR_FOR_FIELD")
		return false
	}

	if isNumericFieldWithBadOperator {
		msg := fmt.Sprintf("Operator '%s' is not valid for numeric field '%s'", condition.Operator, condition.Field)
		addError(result, fieldPrefix+".operator", msg, "INVALID_OPERATOR_FOR_FIELD")
		return false
	}

	return true
}

func validateValueRequirements(operator OperatorType, condition *Condition, fieldPrefix string, result *ValidationResult) {
	needsValuesArray := RequiresValues(operator)
	needsMinMax := RequiresMinMax(operator)

	if needsValuesArray {
		validateValuesArrayOperator(operator, condition, fieldPrefix, result)
		return
	}

	if needsMinMax {
		validateMinMaxOperator(operator, condition, fieldPrefix, result)
		return
	}

	validateRegularOperator(operator, condition, fieldPrefix, result)
}

func validateValuesArrayOperator(_ OperatorType, condition *Condition, fieldPrefix string, result *ValidationResult) {
	if len(condition.Values) == 0 {
		msg := fmt.Sprintf("Operator '%s' requires 'values' array", condition.Operator)
		addError(result, fieldPrefix+".values", msg, "REQUIRED_FIELD")
	}

	if condition.Value != nil {
		msg := fmt.Sprintf("Operator '%s' should use 'values' not 'value'", condition.Operator)
		addError(result, fieldPrefix+".value", msg, "CONFLICTING_FIELDS")
	}
}

func validateMinMaxOperator(_ OperatorType, condition *Condition, fieldPrefix string, result *ValidationResult) {
	if condition.MinValue == nil {
		msg := fmt.Sprintf("Operator '%s' requires 'min_value'", condition.Operator)
		addError(result, fieldPrefix+".min_value", msg, "REQUIRED_FIELD")
	}

	if condition.MaxValue == nil {
		msg := fmt.Sprintf("Operator '%s' requires 'max_value'", condition.Operator)
		addError(result, fieldPrefix+".max_value", msg, "REQUIRED_FIELD")
	}

	hasValidRange := condition.MinValue != nil && condition.MaxValue != nil && *condition.MinValue < *condition.MaxValue
	if condition.MinValue != nil && condition.MaxValue != nil && !hasValidRange {
		addError(result, fieldPrefix+".min_value", "min_value must be less than max_value", "INVALID_RANGE")
	}

	if condition.Value != nil {
		msg := fmt.Sprintf("Operator '%s' should use 'min_value'/'max_value' not 'value'", condition.Operator)
		addError(result, fieldPrefix+".value", msg, "CONFLICTING_FIELDS")
	}
}

func validateRegularOperator(operator OperatorType, condition *Condition, fieldPrefix string, result *ValidationResult) {
	if condition.Value == nil {
		msg := fmt.Sprintf("Operator '%s' requires 'value'", condition.Operator)
		addError(result, fieldPrefix+".value", msg, "REQUIRED_FIELD")
		return
	}

	// Check if value is empty string
	if strVal, ok := condition.Value.(string); ok && strVal == "" {
		msg := fmt.Sprintf("Operator '%s' requires a non-empty value", condition.Operator)
		addError(result, fieldPrefix+".value", msg, "EMPTY_VALUE")
		return
	}

	// For numeric operators, validate that the value can be parsed as a number
	if IsNumericOperator(operator) {
		if _, err := getNumericValue(condition.Value); err != nil {
			msg := fmt.Sprintf("Invalid numeric value for operator '%s': %s", condition.Operator, err.Error())
			addError(result, fieldPrefix+".value", msg, "INVALID_NUMERIC_VALUE")
		}
	}

	if len(condition.Values) > 0 {
		msg := fmt.Sprintf("Operator '%s' should use 'value' not 'values'", condition.Operator)
		addError(result, fieldPrefix+".values", msg, "CONFLICTING_FIELDS")
	}
}

func validateFieldSpecificRules(field FieldType, operator OperatorType, condition *Condition, fieldPrefix string, result *ValidationResult) {
	validateCaseSensitiveRule(field, condition, fieldPrefix, result)
	validateCurrencyRule(condition, fieldPrefix, result)
	validateAmountRules(field, condition, fieldPrefix, result)
	validateTxDirectionRules(field, condition, fieldPrefix, result)
	validateRegexPattern(operator, condition, fieldPrefix, result)
}

func validateCaseSensitiveRule(field FieldType, condition *Condition, fieldPrefix string, result *ValidationResult) {
	isStringFieldWithCaseSensitive := condition.CaseSensitive != nil && !IsStringField(field)
	if isStringFieldWithCaseSensitive {
		addError(result, fieldPrefix+".case_sensitive", "case_sensitive only applies to string fields", "INVALID_FIELD_FOR_TYPE")
	}
}

func validateCurrencyRule(condition *Condition, fieldPrefix string, result *ValidationResult) {
	if condition.Currency != nil {
		addError(result, fieldPrefix+".currency", "currency property is not supported, use currency field instead", "INVALID_FIELD_FOR_TYPE")
	}
}

func validateAmountRules(field FieldType, condition *Condition, fieldPrefix string, result *ValidationResult) {
	if field != FieldAmount {
		return
	}

	if condition.MinValue != nil && *condition.MinValue < 0 {
		addError(result, fieldPrefix+".min_value", "min_value cannot be negative", "INVALID_VALUE")
	}

	if condition.MaxValue != nil && *condition.MaxValue < 0 {
		addError(result, fieldPrefix+".max_value", "max_value cannot be negative", "INVALID_VALUE")
	}
}

func validateTxDirectionRules(field FieldType, condition *Condition, fieldPrefix string, result *ValidationResult) {
	if field != FieldTxDirection {
		return
	}

	if condition.Value != nil {
		if numValue, err := getNumericValue(condition.Value); err == nil {
			isValidDirection := numValue >= 0 && numValue <= 2
			if !isValidDirection {
				addError(result, fieldPrefix+".value", "tx_direction must be between 0 and 2", "INVALID_VALUE")
			}
		}
	}

	if condition.MinValue != nil {
		isValidMin := *condition.MinValue >= 0 && *condition.MinValue <= 2
		if !isValidMin {
			addError(result, fieldPrefix+".min_value", "tx_direction must be between 0 and 2", "INVALID_VALUE")
		}
	}

	if condition.MaxValue != nil {
		isValidMax := *condition.MaxValue >= 0 && *condition.MaxValue <= 2
		if !isValidMax {
			addError(result, fieldPrefix+".max_value", "tx_direction must be between 0 and 2", "INVALID_VALUE")
		}
	}
}

func validateRegexPattern(operator OperatorType, condition *Condition, fieldPrefix string, result *ValidationResult) {
	if operator != OpRegex || condition.Value == nil {
		return
	}

	strValue, err := getStringValue(condition.Value)
	if err != nil {
		return
	}

	_, regexErr := regexp.Compile(strValue)
	if regexErr != nil {
		msg := fmt.Sprintf("Invalid regex pattern: %s", regexErr.Error())
		addError(result, fieldPrefix+".value", msg, "INVALID_REGEX")
	}
}

func addError(result *ValidationResult, field, message, code string) {
	result.Valid = false
	result.Errors = append(result.Errors, ValidationError{
		Field:   field,
		Message: message,
		Code:    code,
	})
}

// NormalizeAndValidateRule normalizes and validates a rule, returning the normalized version
func NormalizeAndValidateRule(jsonData []byte) (*RuleConditions, error) {
	rule, err := ParseRuleConditions(jsonData)
	if err != nil {
		return nil, err
	}

	// Additional normalization
	rule.Logic = strings.ToUpper(rule.Logic)

	for i := range rule.Conditions {
		condition := &rule.Conditions[i]

		// Set default case sensitivity for string fields
		if IsStringField(FieldType(condition.Field)) && condition.CaseSensitive == nil {
			defaultCase := false
			condition.CaseSensitive = &defaultCase
		}

		// Normalize string values if not case sensitive
		if IsStringField(FieldType(condition.Field)) && condition.CaseSensitive != nil && !*condition.CaseSensitive {
			if condition.Value != nil {
				if strValue, err := getStringValue(condition.Value); err == nil {
					condition.Value = strings.ToLower(strValue)
				}
			}
			for j := range condition.Values {
				condition.Values[j] = strings.ToLower(condition.Values[j])
			}
		}
	}

	return rule, nil
}

// GetValidationSchema returns a JSON schema description for frontend validation
func GetValidationSchema() map[string]interface{} {
	var schema map[string]interface{}
	if err := json.Unmarshal(ruleSchemaJSON, &schema); err != nil {
		return map[string]interface{}{}
	}

	return schema
}
