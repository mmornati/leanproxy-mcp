# Story 1-2: User Registration

## User Story
As a visitor, I want to register a new account so that I can become a user.

## Acceptance Criteria
- Given a visitor on the registration page
- When the visitor fills out the registration form with valid data
- Then a new user account is created and the visitor is logged in

## Technical Notes
- Password must be at least 8 characters
- Email must be validated
- Use bcrypt for password hashing

## Implementation Tasks
- [ ] Create registration form component
- [ ] Implement registration API endpoint
- [ ] Add email validation
- [ ] Implement password hashing
- [ ] Auto-login after registration

## Dependencies
- Story 1-1 (User Login) - for session handling patterns

## Status
backlog