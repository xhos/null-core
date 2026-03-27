- [x] `foreign_amount.units` is serialized with locale-formatted commas (e.g. `"4,955"` instead of `4955`) — fixed: backup package now uses `MoneyJSON` with plain integer `units` and an `UnmarshalJSON` that strips commas from string-encoded values
- [x] `units` fields on Money types come out as strings (`"600"`) via protojson int64 encoding — document this for consumers or switch to a custom JSON representation that uses numeric types
- [x] implement account aliasing at DB level. with some banks, when you re-issue a card you get a new number in statments in emails even if its the same account. we should make it possible to essentially say: acount with number 1234 is the same as the account with number 4321. This would probably most conviniently done as adding an array row to the accounts table for aliass, with checks to disallow circular references. Current functionality like this existing in the email-parser and statment-parser and should be stripped out

- [ ] an endpoint for the list of supported currencies

- [ ] integration and unit testing
  - [ ] db tests
    - [x] balance
    - [ ] account aliasing
  - [ ] service tests
    - [ ] balance tests
    - [ ] account aliasing
