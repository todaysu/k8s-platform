# Cluster Onboarding Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build the first usable slice of the lightweight Kubernetes multi-cluster platform: one administrator can add, inspect, rename, re-check, update, and remove a cluster using a cluster name and a token-based Kubeconfig.

**Architecture:** Use a Django 5.2 LTS modular monolith with server-rendered templates and SQLite during local development. Keep Kubernetes parsing, connection checks, and credential encryption in independent service modules. Test and confirmation are separated by a short-lived encrypted database draft, so raw Kubeconfig data never needs to be returned to the browser.

**Tech Stack:** Python 3.12, Django 5.2 LTS, PyYAML, cryptography/Fernet, Python standard-library HTTPS client, pytest, pytest-django, Bootstrap via local template styles.

---

## Before starting

Read the approved design first:

```bash
sed -n '1,260p' docs/plans/2026-10-13-cluster-onboarding-design.md
```

Work through one task at a time. Do not add nodes, Pods, Events, health scoring, alerts, Exec, REST framework, Redis, Celery, PostgreSQL, or Kubernetes deployment manifests in this implementation.

## Target project structure

```text
.
├── .env.example
├── .gitignore
├── README.md
├── manage.py
├── requirements.txt
├── config/
│   ├── __init__.py
│   ├── settings.py
│   ├── urls.py
│   └── wsgi.py
├── clusters/
│   ├── __init__.py
│   ├── admin.py
│   ├── apps.py
│   ├── forms.py
│   ├── models.py
│   ├── urls.py
│   ├── views.py
│   ├── migrations/
│   ├── management/commands/cleanup_cluster_drafts.py
│   ├── services/
│   │   ├── connection.py
│   │   ├── crypto.py
│   │   ├── errors.py
│   │   ├── fingerprint.py
│   │   ├── imports.py
│   │   └── kubeconfig.py
│   ├── templates/clusters/
│   │   ├── base.html
│   │   ├── cluster_confirm_delete.html
│   │   ├── cluster_detail.html
│   │   ├── cluster_form.html
│   │   ├── cluster_list.html
│   │   └── cluster_preview.html
│   └── tests/
│       ├── factories.py
│       ├── test_connection.py
│       ├── test_crypto.py
│       ├── test_import_service.py
│       ├── test_kubeconfig.py
│       ├── test_models.py
│       └── test_views.py
└── templates/registration/login.html
```

---

### Task 1: Bootstrap Django and prove the app runs

**Files:**

- Create: `requirements.txt`
- Create: `.gitignore`
- Create: `.env.example`
- Create: `README.md`
- Create: `manage.py`
- Create: `config/__init__.py`
- Create: `config/settings.py`
- Create: `config/urls.py`
- Create: `config/wsgi.py`
- Create: `clusters/__init__.py`
- Create: `clusters/apps.py`
- Create: `clusters/urls.py`
- Create: `clusters/views.py`
- Create: `clusters/tests/test_views.py`

**Step 1: Create and activate the virtual environment**

Run:

```bash
python3.12 -m venv .venv
source .venv/bin/activate
```

Expected: `python --version` reports Python 3.12.x.

**Step 2: Add dependencies**

Create `requirements.txt`:

```text
Django>=5.2,<5.3
PyYAML>=6,<7
cryptography>=45,<47
pytest>=8,<10
pytest-django>=4.11,<5
```

Run:

```bash
pip install -r requirements.txt
```

Expected: installation completes without errors.

**Step 3: Add repository exclusions**

Create `.gitignore`:

```text
.venv/
__pycache__/
*.py[cod]
*.sqlite3
.env
.pytest_cache/
htmlcov/
.coverage
```

Create `.env.example`:

```text
DJANGO_SECRET_KEY=replace-me
FIELD_ENCRYPTION_KEY=replace-with-a-fernet-key
DJANGO_DEBUG=true
```

**Step 4: Generate the Django project and app skeleton**

Use Django generators rather than typing boilerplate:

```bash
django-admin startproject config .
python manage.py startapp clusters
```

Then add `clusters` to `INSTALLED_APPS`, set `TEMPLATES[0]["DIRS"]` to `[BASE_DIR / "templates"]`, and configure:

```python
LOGIN_URL = "login"
LOGIN_REDIRECT_URL = "cluster-list"
LOGOUT_REDIRECT_URL = "login"
```

Read secrets from environment without supplying production defaults:

```python
SECRET_KEY = os.environ.get("DJANGO_SECRET_KEY", "development-only-secret")
FIELD_ENCRYPTION_KEY = os.environ.get("FIELD_ENCRYPTION_KEY", "")
DEBUG = os.environ.get("DJANGO_DEBUG", "false").lower() == "true"
```

**Step 5: Write the first failing route test**

Add `pytest.ini`:

```ini
[pytest]
DJANGO_SETTINGS_MODULE = config.settings
python_files = test_*.py
```

Add to `clusters/tests/test_views.py`:

```python
import pytest
from django.urls import reverse


@pytest.mark.django_db
def test_cluster_list_requires_login(client):
    response = client.get(reverse("cluster-list"))
    assert response.status_code == 302
    assert reverse("login") in response.url
```

Run:

```bash
pytest clusters/tests/test_views.py -v
```

Expected: FAIL because `cluster-list` does not exist.

**Step 6: Implement the minimum route**

In `clusters/views.py`:

```python
from django.contrib.auth.decorators import login_required
from django.http import HttpResponse


@login_required
def cluster_list(request):
    return HttpResponse("Clusters")
```

In `clusters/urls.py`:

```python
from django.urls import path
from . import views

urlpatterns = [path("", views.cluster_list, name="cluster-list")]
```

In `config/urls.py`, include authentication and cluster routes:

```python
from django.contrib import admin
from django.urls import include, path

urlpatterns = [
    path("admin/", admin.site.urls),
    path("accounts/", include("django.contrib.auth.urls")),
    path("clusters/", include("clusters.urls")),
]
```

Run:

```bash
python manage.py migrate
pytest -v
```

Expected: PASS.

**Step 7: Commit**

```bash
git add .gitignore .env.example README.md requirements.txt pytest.ini manage.py config clusters
git commit -m "chore: bootstrap Django cluster platform"
```

---

### Task 2: Add the administrator login and base pages

**Files:**

- Create: `templates/registration/login.html`
- Create: `clusters/templates/clusters/base.html`
- Create: `clusters/templates/clusters/cluster_list.html`
- Modify: `clusters/views.py`
- Test: `clusters/tests/test_views.py`

**Step 1: Write failing authentication tests**

Add tests that:

```python
@pytest.mark.django_db
def test_logged_in_admin_can_open_cluster_list(client, django_user_model):
    admin = django_user_model.objects.create_superuser(
        username="admin", email="admin@example.invalid", password="test-password"
    )
    client.force_login(admin)
    response = client.get(reverse("cluster-list"))
    assert response.status_code == 200
    assert "尚未纳管任何集群" in response.content.decode()


@pytest.mark.django_db
def test_non_staff_user_is_rejected(client, django_user_model):
    user = django_user_model.objects.create_user("viewer", password="test-password")
    client.force_login(user)
    response = client.get(reverse("cluster-list"))
    assert response.status_code == 403
```

Run the tests and expect failure because the template and staff check are absent.

**Step 2: Add a reusable administrator guard**

Use `@user_passes_test(lambda user: user.is_active and user.is_staff)` together with `@login_required` on all cluster views. Do not create a custom user model in this version.

**Step 3: Add server-rendered templates**

`base.html` must contain navigation, the logged-in username, logout form using POST, and Django messages. `cluster_list.html` must extend it and show the empty state plus an “添加集群” link.

`login.html` must contain only username, password, CSRF token, and submit button. Do not add registration or password-reset links.

**Step 4: Run tests and create the administrator**

```bash
pytest clusters/tests/test_views.py -v
python manage.py createsuperuser
python manage.py runserver
```

Expected: login succeeds and `/clusters/` shows the empty state.

**Step 5: Commit**

```bash
git add templates clusters/templates clusters/views.py clusters/tests/test_views.py
git commit -m "feat: add single administrator login"
```

---

### Task 3: Add cluster, draft, and operation-log models

**Files:**

- Create: `clusters/models.py`
- Create: `clusters/admin.py`
- Create: `clusters/tests/test_models.py`
- Create: migration generated by Django

**Step 1: Write failing model tests**

Cover:

```python
@pytest.mark.django_db
def test_cluster_name_and_fingerprint_are_unique():
    # Create one Cluster, then assert duplicate name and duplicate fingerprint
    # each raise django.db.IntegrityError.
    ...


@pytest.mark.django_db
def test_draft_expires_after_ten_minutes():
    draft = ClusterImportDraft.objects.create(...)
    assert draft.is_expired is False
```

Run:

```bash
pytest clusters/tests/test_models.py -v
```

Expected: import failure because models are absent.

**Step 2: Implement enums and models**

Use UUID primary keys and Django `TextChoices`:

```python
class ConnectionStatus(models.TextChoices):
    CONNECTED = "connected", "正常"
    UNREACHABLE = "unreachable", "无法连接"
    AUTH_FAILED = "authentication_failed", "认证失败"
    TLS_FAILED = "tls_failed", "证书失败"


class Cluster(models.Model):
    id = models.UUIDField(primary_key=True, default=uuid.uuid4, editable=False)
    name = models.CharField(max_length=50, unique=True)
    api_server = models.URLField(max_length=500)
    context_name = models.CharField(max_length=253)
    ca_certificate = models.TextField()
    encrypted_token = models.TextField()
    kubernetes_version = models.CharField(max_length=64)
    fingerprint = models.CharField(max_length=64, unique=True)
    connection_status = models.CharField(
        max_length=32, choices=ConnectionStatus.choices, default=ConnectionStatus.CONNECTED
    )
    last_error_code = models.CharField(max_length=64, blank=True)
    last_checked_at = models.DateTimeField()
    credentials_updated_at = models.DateTimeField()
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)
```

`ClusterImportDraft` holds the same parsed connection fields, a nullable `cluster` foreign key for updates, and `expires_at`. Its `is_expired` property compares `timezone.now()` with `expires_at`.

`OperationLog` stores action, result, error code, cluster UUID snapshot, name snapshot, and creation time. Do not add request bodies or generic JSON metadata.

**Step 3: Generate and inspect migration**

```bash
python manage.py makemigrations clusters
python manage.py sqlmigrate clusters 0001
python manage.py migrate
```

Expected: three tables and uniqueness constraints are present.

**Step 4: Register safe fields in Django admin**

Register models, but exclude `encrypted_token` and CA contents from list and readonly displays. Do not make drafts editable.

**Step 5: Run tests and commit**

```bash
pytest clusters/tests/test_models.py -v
git add clusters/models.py clusters/admin.py clusters/migrations clusters/tests/test_models.py
git commit -m "feat: add cluster onboarding models"
```

---

### Task 4: Parse only the supported Kubeconfig subset

**Files:**

- Create: `clusters/services/__init__.py`
- Create: `clusters/services/errors.py`
- Create: `clusters/services/kubeconfig.py`
- Create: `clusters/services/fingerprint.py`
- Create: `clusters/tests/factories.py`
- Create: `clusters/tests/test_kubeconfig.py`

**Step 1: Define safe domain errors**

Create:

```python
class ClusterOnboardingError(Exception):
    def __init__(self, code: str):
        self.code = code
        super().__init__(code)
```

Views must display a translated message by error code, never `str()` from third-party exceptions.

**Step 2: Create a valid Kubeconfig test factory**

The factory should produce YAML with two contexts and a configurable `current-context`, an HTTPS server, `certificate-authority-data`, and token. Use a small test CA fixture rather than production data.

**Step 3: Write parameterized failing parser tests**

Test valid parsing plus every rejection:

- empty or invalid YAML;
- missing/invalid `current-context`;
- missing cluster or user reference;
- non-HTTPS server;
- missing/invalid base64 CA;
- missing token;
- `tokenFile`, `exec`, client certificate, username/password only;
- document larger than 1 MiB.

**Step 4: Implement the parser**

Return a frozen dataclass:

```python
@dataclass(frozen=True)
class ParsedKubeconfig:
    api_server: str
    context_name: str
    ca_pem: str
    token: str
```

Use `yaml.safe_load`. Resolve items by their `name` field. Decode CA with strict base64 validation. Confirm that decoded data contains PEM certificate markers. Normalize the API Server by removing only a trailing slash.

**Step 5: Add fingerprinting**

```python
def cluster_fingerprint(api_server: str, ca_pem: str) -> str:
    material = f"{api_server.rstrip('/')}\0{ca_pem.strip()}".encode()
    return hashlib.sha256(material).hexdigest()
```

Run:

```bash
pytest clusters/tests/test_kubeconfig.py -v
```

Expected: all parser tests pass.

**Step 6: Commit**

```bash
git add clusters/services clusters/tests/factories.py clusters/tests/test_kubeconfig.py
git commit -m "feat: parse restricted token kubeconfigs"
```

---

### Task 5: Encrypt tokens and test Kubernetes connectivity

**Files:**

- Create: `clusters/services/crypto.py`
- Create: `clusters/services/connection.py`
- Create: `clusters/tests/test_crypto.py`
- Create: `clusters/tests/test_connection.py`
- Modify: `config/settings.py`

**Step 1: Write encryption tests**

Assert that encrypting a token does not include plaintext, decrypting restores it, and a missing/invalid key raises `ENCRYPTION_FAILED` without exposing the token.

**Step 2: Implement Fernet encryption**

```python
def encrypt_secret(value: str) -> str:
    try:
        return Fernet(settings.FIELD_ENCRYPTION_KEY.encode()).encrypt(value.encode()).decode()
    except Exception as exc:
        raise ClusterOnboardingError("ENCRYPTION_FAILED") from exc
```

Implement `decrypt_secret` symmetrically. Never log `value` or the exception contents.

Generate a local key:

```bash
python -c "from cryptography.fernet import Fernet; print(Fernet.generate_key().decode())"
```

Export the result as `FIELD_ENCRYPTION_KEY` only in the local shell; do not commit it.

**Step 3: Write mocked connection tests**

Cover successful `/version` JSON plus timeout, refused connection, TLS verification failure, HTTP 401, HTTP 403, redirect, invalid JSON, and response larger than 64 KiB.

**Step 4: Implement the connection checker**

Use `ssl.create_default_context(cadata=ca_pem)` and the standard library HTTPS client with:

- five-second total timeout;
- `Authorization: Bearer ...`;
- no redirect following;
- maximum 64 KiB response;
- URL fixed to `{api_server}/version`;
- mapping of expected failures to approved error codes.

Return only:

```python
@dataclass(frozen=True)
class ClusterVersion:
    git_version: str
```

**Step 5: Run tests and commit**

```bash
pytest clusters/tests/test_crypto.py clusters/tests/test_connection.py -v
git add config/settings.py clusters/services/crypto.py clusters/services/connection.py clusters/tests
git commit -m "feat: encrypt credentials and check cluster API"
```

---

### Task 6: Implement the test-and-confirm import service

**Files:**

- Create: `clusters/services/imports.py`
- Create: `clusters/tests/test_import_service.py`

**Step 1: Write failing service tests**

Test that validation:

- parses and checks a valid Kubeconfig;
- rejects duplicate names and fingerprints;
- encrypts the token before storing the draft;
- creates a ten-minute expiry;
- returns only safe summary data.

Test that confirmation:

- creates one Cluster from a valid draft inside a transaction;
- deletes the draft;
- rejects expired drafts;
- safely handles a duplicate created between validation and confirmation;
- writes a successful or failed OperationLog.

**Step 2: Implement orchestration services**

Expose narrow functions:

```python
def validate_new_cluster(*, name: str, kubeconfig_bytes: bytes) -> ImportPreview:
    ...


@transaction.atomic
def confirm_new_cluster(*, draft_id: UUID) -> Cluster:
    ...
```

`ImportPreview` contains only draft ID, API Server, Context, Kubernetes version, and expiry time. It must not contain Token or CA.

Normalize the display name by trimming surrounding whitespace, but do not silently change internal characters.

**Step 3: Run tests and commit**

```bash
pytest clusters/tests/test_import_service.py -v
git add clusters/services/imports.py clusters/tests/test_import_service.py
git commit -m "feat: add cluster import draft workflow"
```

---

### Task 7: Build add, preview, list, and detail pages

**Files:**

- Create: `clusters/forms.py`
- Create: `clusters/templates/clusters/cluster_form.html`
- Create: `clusters/templates/clusters/cluster_preview.html`
- Create: `clusters/templates/clusters/cluster_detail.html`
- Modify: `clusters/templates/clusters/cluster_list.html`
- Modify: `clusters/views.py`
- Modify: `clusters/urls.py`
- Modify: `clusters/tests/test_views.py`

**Step 1: Write failing form and view tests**

Test both upload and paste modes, rejection when both/neither are present, the 1 MiB file limit, safe error messages, preview contents, confirmation, empty list, populated list, and detail view.

Also assert that response HTML never contains the fixture token or CA PEM.

**Step 2: Implement the form**

```python
class ClusterImportForm(forms.Form):
    name = forms.CharField(max_length=50, label="集群名称")
    kubeconfig_file = forms.FileField(required=False, label="上传文件")
    kubeconfig_text = forms.CharField(
        required=False, widget=forms.Textarea, label="粘贴内容"
    )

    def clean(self):
        cleaned = super().clean()
        supplied = bool(cleaned.get("kubeconfig_file")) + bool(
            cleaned.get("kubeconfig_text", "").strip()
        )
        if supplied != 1:
            raise forms.ValidationError("请选择上传文件或粘贴内容中的一种方式。")
        return cleaned
```

Read uploaded data once, enforce size before parsing, and pass bytes into the service.

**Step 3: Implement POST/Redirect/GET views**

Use named routes:

```text
clusters/                       cluster-list
clusters/add/                   cluster-add
clusters/import/<uuid>/         cluster-preview
clusters/import/<uuid>/confirm/ cluster-confirm
clusters/<uuid>/                cluster-detail
```

Every view requires an authenticated staff user. Confirmation uses POST and CSRF. Successful confirmation redirects to detail, preventing refresh from creating duplicates.

**Step 4: Render plain, usable templates**

Use semantic HTML and a small local stylesheet. Do not use CDN JavaScript. The preview page shows only the approved safe fields and expiry time.

**Step 5: Test and manually exercise**

```bash
pytest clusters/tests/test_views.py -v
python manage.py runserver
```

Expected: a valid test Kubeconfig reaches preview and confirmation; secrets never appear in page source.

**Step 6: Commit**

```bash
git add clusters/forms.py clusters/templates clusters/views.py clusters/urls.py clusters/tests/test_views.py
git commit -m "feat: add cluster onboarding pages"
```

---

### Task 8: Add rename, re-check, and credential replacement

**Files:**

- Modify: `clusters/forms.py`
- Modify: `clusters/services/imports.py`
- Modify: `clusters/views.py`
- Modify: `clusters/urls.py`
- Modify: `clusters/templates/clusters/cluster_detail.html`
- Create: `clusters/templates/clusters/cluster_rename.html`
- Modify: `clusters/tests/test_import_service.py`
- Modify: `clusters/tests/test_views.py`

**Step 1: Write failing tests**

Cover:

- rename success and duplicate-name rejection;
- re-check updates status, version, error code, and time;
- failed re-check stores only the safe error code;
- credential validation links the draft to the existing cluster;
- failed validation leaves the old encrypted token unchanged;
- confirmation replaces all connection fields atomically;
- changing API Server displays a warning before confirmation.

**Step 2: Implement rename and re-check**

Use POST for both. `check` decrypts the current token only inside the service, calls the connection checker, updates status, and discards plaintext immediately.

**Step 3: Reuse the import form for credential update**

The update page does not ask for a new name. Create an update draft associated with the Cluster. Confirmation locks the row with `select_for_update()` before replacing credentials.

**Step 4: Run tests and commit**

```bash
pytest clusters/tests/test_import_service.py clusters/tests/test_views.py -v
git add clusters
git commit -m "feat: update cluster identity and credentials"
```

---

### Task 9: Add safe removal and draft cleanup

**Files:**

- Create: `clusters/templates/clusters/cluster_confirm_delete.html`
- Create: `clusters/management/__init__.py`
- Create: `clusters/management/commands/__init__.py`
- Create: `clusters/management/commands/cleanup_cluster_drafts.py`
- Modify: `clusters/forms.py`
- Modify: `clusters/views.py`
- Modify: `clusters/urls.py`
- Modify: `clusters/tests/test_views.py`
- Create: `clusters/tests/test_cleanup_command.py`

**Step 1: Write failing deletion tests**

Assert that GET only shows confirmation, wrong confirmation text does not delete, exact cluster name deletes the platform record, and no remote connection method is called during deletion.

**Step 2: Implement explicit confirmation**

The form has one field named `confirmation_name`. The view compares it exactly with `cluster.name`, deletes inside a transaction, writes the audit snapshot first, and redirects to the list.

**Step 3: Write and implement cleanup tests**

The management command deletes only expired drafts:

```bash
python manage.py cleanup_cluster_drafts
```

Output only the number of deleted drafts, never draft contents.

**Step 4: Run tests and commit**

```bash
pytest clusters/tests/test_views.py clusters/tests/test_cleanup_command.py -v
git add clusters
git commit -m "feat: safely remove clusters and expired drafts"
```

---

### Task 10: Security regression and end-to-end verification

**Files:**

- Create: `clusters/tests/test_security.py`
- Modify: `config/settings.py`
- Modify: `README.md`

**Step 1: Add security regression tests**

Test:

- every cluster route redirects anonymous users;
- logged-in non-staff users receive 403;
- state-changing requests require POST and CSRF;
- unsafe HTTP API Servers are rejected;
- redirects from API Servers are rejected;
- Token and CA never appear in HTML, messages, OperationLog, or captured application logs;
- `DEBUG` defaults to false when the environment variable is absent;
- the app refuses to start a credential operation without a valid encryption key.

**Step 2: Add production-safe settings**

When `DEBUG` is false, require `DJANGO_SECRET_KEY`, set secure session and CSRF cookies, and read allowed hosts from an environment variable. Keep local defaults only when `DEBUG=true` is explicitly set.

**Step 3: Document local setup**

`README.md` must include:

1. Install Python 3.12.
2. Create `.venv` and install requirements.
3. Generate `DJANGO_SECRET_KEY` and `FIELD_ENCRYPTION_KEY`.
4. Run migrations.
5. Create the single superuser.
6. Start the server.
7. Prepare a read-only, token-based test Kubeconfig with inline CA data.
8. Run all tests.

Do not put a real key or Kubeconfig in the README.

**Step 4: Run final verification**

```bash
python manage.py check --deploy
pytest -v
python manage.py cleanup_cluster_drafts
git status --short
```

Expected:

- deployment check has no unreviewed critical warning;
- all tests pass;
- cleanup runs without exposing data;
- only intended files are changed.

**Step 5: Commit**

```bash
git add README.md config/settings.py clusters/tests/test_security.py
git commit -m "test: verify cluster onboarding security"
```

---

## Final manual acceptance

Use a non-production test cluster and work through the approved acceptance list:

1. Log in as the only administrator.
2. Add the cluster by file upload.
3. Remove it, then add it by pasted text.
4. Confirm that a multi-context file uses `current-context`.
5. Try invalid current Context, CA, Token, and unreachable API.
6. Try to add the same cluster with another display name.
7. Update with an invalid Kubeconfig and verify the old credential still works.
8. Update with a valid new Token.
9. Rename the display name.
10. Remove the platform record and verify the real cluster still runs.
11. Search HTML, application logs, database operation logs, and Git history for the test Token; it must not appear.

Only after these checks pass should development move to the next feature, “集群列表与周期连接状态”。
