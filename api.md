# AIG Custom Platform API Documentation


## Overview

AIG Custom Platform is an independent custom platform based on Tencent Zhuque Lab AI-Infra-Guard (https://github.com/Tencent/AI-Infra-Guard). It provides a comprehensive set of API interfaces for Agent Scan, MCP Server Scan, Jailbreak Evaluation, AI Infra Scan, and Model Configuration Management. This documentation details the usage methods, parameter descriptions, and example code for each API interface.

After the project is running, you can access `http://localhost:8088/docs/index.html` to view the Swagger documentation.

## Table of Contents

### Protected Platform Tasks
- Cookie session authentication and Subject authorization
- Platform task collection and owner-scoped operations
- Opaque private attachments
- Retired browser task migration boundary

### Model Management API
1. Get Model List
2. Get Model Detail
3. Create Model
4. Update Model
5. Delete Model
6. YAML Configuration Models

### Operations
- Error handling
- Migration and deployment notes

## Basic Information

- **Base URL**: `http://localhost:8088` (adjust according to actual deployment)
- **Content-Type**: `application/json`
- **Authentication**: Browser requests use the `aig_session` HttpOnly cookie issued by the session login endpoint. Do not send a `username` header: it is not an authentication mechanism. State-changing authenticated requests must also send the `X-CSRF-Token` header matching the `aig_csrf` cookie.

## Common Response Format

All API interfaces follow a unified response format:

```json
{
  "status": 0,           // Status code: 0=success, 1=failure
  "message": "Operation successful",  // Response message
  "data": {}             // Response data
}
```

## Task API migration boundary

Browser task execution is available only through the protected platform task API described above. The former `/api/v1/app/taskapi*` and `/api/v1/app/tasks*` browser families are historical names, are not callable compatibility APIs, and return `410 Gone` only after the normal session, password-change and CSRF checks (CSRF applies to mutating requests). They cannot be used for task creation, upload, status, results, streaming, or as a fallback after a broken connection.

Use governed model IDs with the platform task create endpoint. Raw model credentials and legacy model objects are rejected. Browser attachments are referenced only by opaque attachment IDs and remain owner-scoped. The internal Agent WebSocket and legacy-shaped artifact transport are a separate internal-token boundary and are not browser APIs.

### Protected platform boundary

Browser clients authenticate with the secure Cookie session. Anonymous requests receive `401`; authenticated Subjects without permission receive `403`. Identity and role headers are never an authentication fallback.

Platform tasks use `GET /api/v1/platform/tasks`, `POST /api/v1/platform/tasks`, and owner-authorized detail, cancel, result, and opaque attachment operations. Creation requires `Idempotency-Key`; task ownership always comes from the authenticated Subject. Users see only their own tasks, auditors have global read-only access, and administrators may govern all tasks. A network acknowledgement that cannot be proven becomes `dispatch_unknown` and is never submitted automatically again.

The independently governed model API is `/api/v1/platform/models`. The deprecated `/api/v1/app/models/{modelId}` facade remains only for model compatibility: collection DELETE and its nested request bodies retain the `{status,message,data}` envelope and HTTP `200` application-error convention. Response credentials are masked. YAML model IDs cannot shadow encrypted platform rows, and YAML loading fails closed before any database or audit mutation.

Only `aig migrate` may apply database DDL. A fresh or upgraded PostgreSQL database schema reaches v6; runtime startup only validates it. Migration safely handles an empty legacy table and never relies on runtime AutoMigrate.

## Model Management API

> **Deprecated compatibility API.** The endpoints in this section preserve the browser contract (`GET`/`POST`/collection `DELETE` on `/api/v1/app/models`, and detail `GET`/`PUT` on `/api/v1/app/models/{modelId}`). Requests require the authenticated session Subject and CSRF protection for mutations; a `username` header is ignored. POST and PUT keep the nested `model` object, DELETE keeps `{ "model_ids": [...] }`, and all operations use the legacy HTTP `200` envelope described above. List/detail merge read-only YAML models, preserve their `default` string arrays, and return an empty array for encrypted platform models. Tokens are always `********`; YAML models cannot be mutated or shadowed by a platform row. Duplicate YAML IDs return `status: 1`, and a YAML load error fails closed before database or audit writes. Use `/api/v1/platform/models` for the non-deprecated flat contract.

### 1. Get Model List

#### Interface Information
- **URL**: `/api/v1/app/models`
- **Method**: `GET`
- **Content-Type**: `application/json`

#### Response Fields
| Field | Type | Description |
|-------|------|-------------|
| model_id | string | Model ID |
| model | object | Model configuration information |
| model.model | string | Model name |
| model.token | string | API key (masked as ********) |
| model.base_url | string | Base URL |
| model.note | string | Note information |
| model.limit | integer | Request limit |
| default | array | Task-type defaults from YAML; encrypted platform models return an empty array |

#### Python Example
```python
import requests

def get_model_list():
    url = "http://localhost:8088/api/v1/app/models"
    headers = {
        "Content-Type": "application/json"
    }
    
    response = requests.get(url, headers=headers)
    return response.json()

# Usage example
result = get_model_list()
if result['status'] == 0:
    print("Model list retrieved successfully:")
    for model in result['data']:
        print(f"Model ID: {model['model_id']}")
        print(f"Model Name: {model['model']['model']}")
        print(f"Base URL: {model['model']['base_url']}")
        print(f"Note: {model['model']['note']}")
        print("---")
```

#### cURL Example
```bash
curl -X GET http://localhost:8088/api/v1/app/models \
  -H "Content-Type: application/json"
```

#### Response Example
```json
{
  "status": 0,
  "message": "获取模型列表成功",
  "data": [
    {
      "model_id": "gpt4-model",
      "model": {
        "model": "gpt-4",
        "token": "********",
        "base_url": "https://api.openai.com/v1",
        "note": "GPT-4 Model",
        "limit": 1000
      },
      "default": []
    },
    {
      "model_id": "system_default",
      "model": {
        "model": "deepseek-chat",
        "token": "********",
        "base_url": "https://api.deepseek.com/v1",
        "note": "System Default Model",
        "limit": 1000
      },
      "default": ["mcp_scan", "ai_infra_scan"]
    }
  ]
}
```

### 2. Get Model Detail

#### Interface Information
- **URL**: `/api/v1/app/models/{modelId}`
- **Method**: `GET`
- **Content-Type**: `application/json`

#### Parameter Description
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| modelId | string | Yes | Model ID (path parameter) |

#### Response Fields
| Field | Type | Description |
|-------|------|-------------|
| model_id | string | Model ID |
| model | object | Model configuration information |
| model.model | string | Model name |
| model.token | string | API key (masked as ********) |
| model.base_url | string | Base URL |
| model.note | string | Note information |
| model.limit | integer | Request limit |
| default | array | Task-type defaults from YAML; encrypted platform models return an empty array |

#### Python Example
```python
def get_model_detail(model_id):
    url = f"http://localhost:8088/api/v1/app/models/{model_id}"
    headers = {
        "Content-Type": "application/json"
    }
    
    response = requests.get(url, headers=headers)
    return response.json()

# Usage example
result = get_model_detail("gpt4-model")
if result['status'] == 0:
    model_data = result['data']
    print(f"Model ID: {model_data['model_id']}")
    print(f"Model Name: {model_data['model']['model']}")
    print(f"Base URL: {model_data['model']['base_url']}")
    print(f"Note: {model_data['model']['note']}")
```

#### cURL Example
```bash
curl -X GET http://localhost:8088/api/v1/app/models/gpt4-model \
  -H "Content-Type: application/json"
```

#### Response Example
```json
{
  "status": 0,
  "message": "Get model detail successfully",
  "data": {
    "model_id": "gpt4-model",
    "model": {
      "model": "gpt-4",
      "token": "********",
      "base_url": "https://api.openai.com/v1",
      "note": "GPT-4 Model",
      "limit": 1000
    },
    "default": []
  }
}
```

### 3. Create Model

#### Interface Information
- **URL**: `/api/v1/app/models`
- **Method**: `POST`
- **Content-Type**: `application/json`

#### Request Parameters
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| model_id | string | Yes | Model ID, globally unique |
| model | object | Yes | Model configuration information |
| model.model | string | Yes | Model name |
| model.token | string | Yes | API key |
| model.base_url | string | Yes | Base URL |
| model.note | string | No | Note information |
| model.limit | integer | No | Request limit, default 1000 |

`model_id` is trimmed before comparison with the read-only YAML source. A matching YAML ID cannot shadow that shared model: the API returns HTTP `200` with `status: 1` and performs no database or audit mutation. If the YAML source cannot be loaded, creation fails closed with the same envelope and also performs no write.

#### Python Example
```python
def create_model():
    url = "http://localhost:8088/api/v1/app/models"
    headers = {
        "Content-Type": "application/json"
    }
    data = {
        "model_id": "my-gpt4-model",
        "model": {
            "model": "gpt-4",
            "token": "sk-your-api-key-here",
            "base_url": "https://api.openai.com/v1",
            "note": "My GPT-4 Model",
            "limit": 2000
        }
    }
    
    response = requests.post(url, json=data, headers=headers)
    return response.json()

# Usage example
result = create_model()
if result['status'] == 0:
    print("Model created successfully")
else:
    print(f"Model creation failed: {result['message']}")
```

#### cURL Example
```bash
curl -X POST http://localhost:8088/api/v1/app/models \
  -H "Content-Type: application/json" \
  -d '{
    "model_id": "my-gpt4-model",
    "model": {
      "model": "gpt-4",
      "token": "sk-your-api-key-here",
      "base_url": "https://api.openai.com/v1",
      "note": "My GPT-4 Model",
      "limit": 2000
    }
  }'
```

#### Response Example
```json
{
  "status": 0,
  "message": "Model created successfully",
  "data": null
}
```

### 4. Update Model

#### Interface Information
- **URL**: `/api/v1/app/models/{modelId}`
- **Method**: `PUT`
- **Content-Type**: `application/json`

#### Parameter Description
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| modelId | string | Yes | Model ID (path parameter) |
| model | object | Yes | Model configuration information |
| model.model | string | No | Model name |
| model.token | string | No | API key (pass ******** or empty to keep original value) |
| model.base_url | string | No | Base URL |
| model.note | string | No | Note information |
| model.limit | integer | No | Request limit |

**Note**: 
- If the token field is passed as `********` or empty, the token will not be updated and the original value will be kept
- Supports partial field updates; fields not passed will retain their original values

#### Python Example
```python
def update_model(model_id):
    url = f"http://localhost:8088/api/v1/app/models/{model_id}"
    headers = {
        "Content-Type": "application/json"
    }
    # Only update note and limit, don't modify token
    data = {
        "model": {
            "model": "gpt-4-turbo",
            "token": "********",  # Keep original token
            "base_url": "https://api.openai.com/v1",
            "note": "Updated note information",
            "limit": 3000
        }
    }
    
    response = requests.put(url, json=data, headers=headers)
    return response.json()

# Usage example
result = update_model("my-gpt4-model")
if result['status'] == 0:
    print("Model updated successfully")
else:
    print(f"Model update failed: {result['message']}")
```

#### Update Token Example
```python
def update_model_token(model_id, new_token):
    url = f"http://localhost:8088/api/v1/app/models/{model_id}"
    data = {
        "model": {
            "model": "gpt-4",
            "token": new_token,  # Pass new token
            "base_url": "https://api.openai.com/v1",
            "note": "Updated API key",
            "limit": 2000
        }
    }
    
    response = requests.put(url, json=data)
    return response.json()
```

#### cURL Example
```bash
# Only update note information
curl -X PUT http://localhost:8088/api/v1/app/models/my-gpt4-model \
  -H "Content-Type: application/json" \
  -d '{
    "model": {
      "model": "gpt-4-turbo",
      "token": "********",
      "base_url": "https://api.openai.com/v1",
      "note": "Updated note information",
      "limit": 3000
    }
  }'

# Update token
curl -X PUT http://localhost:8088/api/v1/app/models/my-gpt4-model \
  -H "Content-Type: application/json" \
  -d '{
    "model": {
      "model": "gpt-4",
      "token": "sk-new-api-key-here",
      "base_url": "https://api.openai.com/v1",
      "note": "Updated API key",
      "limit": 2000
    }
  }'
```

#### Response Example
```json
{
  "status": 0,
  "message": "Model updated successfully",
  "data": null
}
```

### 5. Delete Model

#### Interface Information
- **URL**: `/api/v1/app/models`
- **Method**: `DELETE`
- **Content-Type**: `application/json`

#### Request Parameters
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| model_ids | array | Yes | List of model IDs to delete, supports batch deletion |

#### Python Example
```python
def delete_models(model_ids):
    url = "http://localhost:8088/api/v1/app/models"
    headers = {
        "Content-Type": "application/json"
    }
    data = {
        "model_ids": model_ids
    }
    
    response = requests.delete(url, json=data, headers=headers)
    return response.json()

# Delete single model
result = delete_models(["my-gpt4-model"])
if result['status'] == 0:
    print("Model deleted successfully")

# Batch delete multiple models
result = delete_models(["model1", "model2", "model3"])
if result['status'] == 0:
    print("Batch deletion successful")
```

#### cURL Example
```bash
# Delete single model
curl -X DELETE http://localhost:8088/api/v1/app/models \
  -H "Content-Type: application/json" \
  -d '{
    "model_ids": ["my-gpt4-model"]
  }'

# Batch delete multiple models
curl -X DELETE http://localhost:8088/api/v1/app/models \
  -H "Content-Type: application/json" \
  -d '{
    "model_ids": ["model1", "model2", "model3"]
  }'
```

#### Response Example
```json
{
  "status": 0,
  "message": "Deletion successful",
  "data": null
}
```

### 6. YAML Configuration Models

In addition to database models created through the API, the system also supports defining system-level models through YAML configuration files.

#### Configuration File Location
`db/model.yaml`

#### YAML Configuration Format
```yaml
- model_id: system_default
  model_name: deepseek-chat
  token: sk-your-api-key
  base_url: https://api.deepseek.com/v1
  note: System Default Model
  limit: 1000
  default:
    - mcp_scan
    - ai_infra_scan

- model_id: eval_model
  model_name: gpt-4
  token: sk-your-eval-key
  base_url: https://api.openai.com/v1
  note: Evaluation Model
  limit: 2000
  default:
    - model_redteam_report
```

#### Field Description
| Field | Type | Required | Description |
|-------|------|----------|-------------|
| model_id | string | Yes | Model ID |
| model_name | string | Yes | Model name |
| token | string | Yes | API key |
| base_url | string | Yes | Base URL |
| note | string | No | Note information |
| limit | integer | No | Request limit |
| default | array | No | List of task types that use this model by default |

#### Feature Description
- YAML configuration models are **read-only** and cannot be modified or deleted through the API
- YAML configuration models are merged with database models when retrieving lists and details
- The `default` field is unique to YAML models and is used to identify the default task types for which the model is applicable
- YAML configuration is automatically loaded when the system starts

---

## Retired task workflow migration

Do not copy or adapt historical browser task examples. Migrate clients to the platform task collection and owner-authorized detail, cancel, result, and attachment operations documented in the protected platform section. Historical task browser routes return `410 Gone`; there is no WebSocket, SSE, status, result, upload, or credential-bearing request fallback.

## Error Handling

### Common Error Codes
| Status Code | Description | Solution |
|-------------|-------------|----------|
| 0 | Success | - |
| 1 | Failure | Check the message field for detailed error information |

### Error Handling Example
```python
def handle_api_response(response):
    """Common function for handling API responses"""
    data = response.json()
    
    if data['status'] == 0:
        return data['data']
    else:
        raise Exception(f"API call failed: {data['message']}")

# Usage example
try:
    result = handle_api_response(response)
    print("Operation successful:", result)
except Exception as e:
    print("Operation failed:", str(e))
```

## Important Notes

### General Notes
1. **Authentication**: Ensure correct authentication information is included in request headers
2. **File Size**: File upload size limits please refer to server configuration
3. **Timeout Settings**: Set reasonable timeout times based on task complexity
4. **Concurrency Limits**: Avoid creating too many tasks simultaneously to prevent affecting system performance
5. **Result Saving**: Save scan results promptly to avoid data loss

### Task-Related Notes
6. **Dataset Selection**: Choose appropriate dataset combinations based on testing requirements
7. **Model Configuration**: Ensure test model and evaluation model configurations are correct

### Model Management Notes
8. **Model ID Uniqueness**: When creating a model, the model_id must be globally unique
9. **Token Security**: API keys are automatically masked as `********` in responses; pay attention to this when displaying and editing on the frontend
10. **Token Updates**: When updating a model, if the token field is empty or `********`, the token will not be updated and the original value will be kept
11. **Model Validation**: The system automatically validates the token and base_url when creating a model
12. **YAML Models**: Models configured through YAML are read-only and cannot be modified or deleted through the API
13. **Batch Deletion**: Model deletion supports passing multiple model_ids for batch deletion
14. **Permission Control**: Administrators can view, modify, and delete models across owners; auditors have global read-only access; users can view, modify, and delete only their own models

## Technical Support

For any issues, please contact the technical support team or refer to the project documentation.
