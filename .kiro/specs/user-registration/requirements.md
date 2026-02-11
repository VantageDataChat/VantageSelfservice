# Requirements Document

## Introduction

本功能为现有的 Helpdesk 自助服务平台添加用户自行注册能力。用户可通过填写用户名、密码（含确认密码）、验证码图片和邮件地址完成注册。注册后需通过邮件确认才能登录。管理员可在系统设置中配置 SMTP 服务用于发送确认邮件。通过 OAuth 方式登录的用户无需邮件确认，可直接使用系统。

## Glossary

- **Registration_System**: 处理用户自行注册的后端模块，包括表单验证、用户创建、确认令牌生成
- **CAPTCHA_Service**: 生成和验证图形验证码的服务模块
- **Email_Service**: 通过 SMTP 发送邮件的服务模块，用于发送注册确认邮件
- **Confirmation_Token**: 用于邮件确认的唯一令牌，包含过期时间
- **Registration_Form**: 前端注册表单，包含用户名、密码、确认密码、验证码和邮件地址字段
- **SMTP_Config**: 管理员在系统设置中配置的 SMTP 服务参数（主机、端口、用户名、密码、发件人地址）

## Requirements

### Requirement 1: 用户注册表单

**User Story:** As a visitor, I want to register an account with username, password, CAPTCHA, and email, so that I can access the helpdesk system.

#### Acceptance Criteria

1. WHEN a visitor navigates to the registration page, THE Registration_Form SHALL display input fields for username, password, confirm password, CAPTCHA image with input, and email address
2. WHEN a visitor submits the registration form with valid data, THE Registration_System SHALL create a new user record with status "unconfirmed" and send a confirmation email
3. WHEN a visitor submits a username that already exists in the database, THE Registration_System SHALL reject the registration and return a descriptive error message
4. WHEN a visitor submits an email address that already exists in the database, THE Registration_System SHALL reject the registration and return a descriptive error message
5. WHEN a visitor submits a password that does not match the confirm password field, THE Registration_Form SHALL prevent submission and display a validation error
6. WHEN a visitor submits an empty or whitespace-only username, THE Registration_System SHALL reject the registration and return a validation error
7. WHEN a visitor submits a password shorter than 6 characters, THE Registration_System SHALL reject the registration and return a validation error

### Requirement 2: CAPTCHA 验证

**User Story:** As a system operator, I want registration to require CAPTCHA verification, so that automated bot registrations are prevented.

#### Acceptance Criteria

1. WHEN the registration page loads, THE CAPTCHA_Service SHALL generate a CAPTCHA image containing random alphanumeric characters and return the image along with a CAPTCHA identifier
2. WHEN a visitor submits the registration form, THE CAPTCHA_Service SHALL validate the submitted CAPTCHA answer against the stored answer for the given identifier
3. IF the CAPTCHA answer is incorrect or expired, THEN THE Registration_System SHALL reject the registration and prompt the visitor to retry with a new CAPTCHA
4. WHEN a visitor clicks the CAPTCHA image, THE CAPTCHA_Service SHALL generate a new CAPTCHA image to replace the current one

### Requirement 3: 邮件确认流程

**User Story:** As a system operator, I want registered users to confirm their email address before logging in, so that only verified users can access the system.

#### Acceptance Criteria

1. WHEN a user registers successfully, THE Email_Service SHALL send a confirmation email containing a unique confirmation link to the registered email address
2. WHEN a user clicks the confirmation link within the validity period, THE Registration_System SHALL update the user status from "unconfirmed" to "confirmed"
3. IF a user clicks an expired confirmation link, THEN THE Registration_System SHALL display an error message and offer to resend the confirmation email
4. IF a user clicks an invalid or already-used confirmation link, THEN THE Registration_System SHALL display an appropriate error message
5. WHILE a user account status is "unconfirmed", THE Registration_System SHALL reject login attempts and inform the user that email confirmation is required
6. WHEN a user requests to resend the confirmation email, THE Email_Service SHALL generate a new Confirmation_Token and send a new confirmation email

### Requirement 4: SMTP 配置管理

**User Story:** As an administrator, I want to configure SMTP settings in the admin panel, so that the system can send confirmation emails to users.

#### Acceptance Criteria

1. THE SMTP_Config SHALL include fields for SMTP host, port, username, password, and sender email address
2. WHEN an administrator saves SMTP settings, THE Email_Service SHALL store the SMTP password using the existing AES-256-GCM encryption mechanism
3. WHEN an administrator views SMTP settings, THE Email_Service SHALL display the SMTP password as masked ("***")
4. WHEN the Email_Service attempts to send an email and SMTP is not configured, THE Email_Service SHALL return a descriptive error indicating SMTP configuration is missing

### Requirement 5: OAuth 用户免确认登录

**User Story:** As a user logging in via OAuth, I want to access the system immediately without email confirmation, so that the OAuth login experience remains seamless.

#### Acceptance Criteria

1. WHEN a user authenticates via OAuth, THE Registration_System SHALL create or update the user record with status "confirmed" and allow immediate login
2. WHEN an OAuth user already exists in the database, THE Registration_System SHALL update the last login timestamp and create a new session

### Requirement 6: 密码存储安全

**User Story:** As a system operator, I want user passwords to be securely hashed, so that user credentials are protected.

#### Acceptance Criteria

1. WHEN a user registers, THE Registration_System SHALL hash the password using bcrypt before storing it in the database
2. WHEN a registered user logs in with username and password, THE Registration_System SHALL verify the password against the stored bcrypt hash
3. THE Registration_System SHALL store the bcrypt hash with the default cost factor

### Requirement 7: 注册用户登录

**User Story:** As a confirmed registered user, I want to log in with my username and password, so that I can access the helpdesk system.

#### Acceptance Criteria

1. WHEN a confirmed user submits valid username and password, THE Registration_System SHALL create a session and redirect the user to the chat page
2. WHEN a user submits an incorrect password, THE Registration_System SHALL reject the login and display an error message
3. WHEN a user submits a username that does not exist, THE Registration_System SHALL reject the login and display a generic error message to avoid user enumeration
