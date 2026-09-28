/** @type {import('stylelint').Config} */
export default {
  extends: ['stylelint-config-standard'],
  rules: {
    // The stylesheets keep small rules on one line and group related rules
    // without blank lines between them; layout is not what this lint is for.
    'declaration-block-single-line-max-declarations': null,
    'rule-empty-line-before': null,
    'comment-empty-line-before': null,
    'at-rule-empty-line-before': null,
    'custom-property-empty-line-before': null,
    // Rules are ordered by component, so a later component's selector often
    // outranks an earlier, unrelated one. The rule reads that as an override.
    'no-descending-specificity': null,
    // Font names keep their capitals, in font stacks held in custom properties too.
    'value-keyword-case': ['lower', {ignoreProperties: ['font-family', 'font'], ignoreKeywords: ['Roboto']}],
  },
};
