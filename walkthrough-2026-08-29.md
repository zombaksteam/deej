# Результати виконання: Додавання режиму Stream PC Mode та конфігурації master_mapping

## Що було реалізовано

### 1. Опція `master_mapping` у конфігурації ([config.yaml](file:///c:/Users/Zombak/Documents/GitHub/deej/config.yaml))
- Додано параметр `master_mapping: 3` одразу після секції `slider_mapping:`.
- Завдяки цьому більше не потрібно тримати два окремі файли конфігурацій:
  - У звичайному режимі слайдери працюють за стандартним `slider_mapping`.
  - У режимі **Stream PC Mode** слайдер з індексом `master_mapping` (за замовчуванням `3`) контролює виключно загальну гучність Master.

```yaml
slider_mapping:
  2: discord.exe
  3:
    - chrome.exe
    - brave.exe
    - mpc-hc64.exe
    - steamwebhelper.exe
    - C:\Program Files (x86)\Steam\steamapps\common\*
    - speed.exe
    - speed2.exe
    - NFSC.exe
  4:
    - Music.UI.exe
    - Microsoft.Media.Player.exe
    - Screamer.exe
    - winamp.exe
  5:
    - obs-browser-page.exe

# slider index that controls master volume when "Stream PC Mode" is active
master_mapping: 3
```

---

### 2. Чекбокс у треї та збереження стану ([pkg/deej/tray.go](file:///c:/Users/Zombak/Documents/GitHub/deej/pkg/deej/tray.go), [pkg/deej/config.go](file:///c:/Users/Zombak/Documents/GitHub/deej/pkg/deej/config.go))
- **Пункт меню у системному треї**:
  - Додано новий пункт `Stream PC Mode` з інтерактивним чекбоксом (`[x]` / `[ ]`).
  - При кліку статус миттєво перемикається, оновлюється прапорець та надсилається системне Windows Toast-повідомлення.
- **Збереження стану (`preferences.yaml`)**:
  - Стан `stream_pc_mode: true / false` автоматично записується у файл `preferences.yaml` у кореневій папці поруч із `deej.exe` та `config.yaml`.
  - При перезапуску програми вибраний режим автоматично завантажується та зберігається.

---

### 3. Логіка виставлення 100% гучності та контролю Master ([pkg/deej/session_map.go](file:///c:/Users/Zombak/Documents/GitHub/deej/pkg/deej/session_map.go))
- **Автоматичне 100% для процесів з шаблону**:
  - При увімкненні **Stream PC Mode** усі процеси, що зазначені у `slider_mapping` або підпадають під його шляхи/маски (наприклад `discord.exe`, `chrome.exe`, ігри зі `Steam\steamapps\common\*`), якщо їхня гучність була меншою за 100%, негайно виставляються у `1.0` (100%).
  - Сторонні незмаплені процеси залишаються недоторканими.
- **Нові процеси при запуску**:
  - Якщо запущено гру або браузер під час активного режиму Stream PC Mode, метод `applyStoredSliderVolume` автоматично перевіряє прив'язку до шаблону і виставляє гучність `100%`.
- **Ексклюзивний контроль Master**:
  - Рух повзунка з індексом `master_mapping` регулює виключно гучність Master.
  - Усі інші повзунки в режимі Stream PC Mode ігноруються і не змінюють гучності додатків.
- **Повернення до звичайного режиму**:
  - При вимкненні режиму Stream PC Mode для всіх процесів миттєво відновлюються їхні збережені значення з відповідних фізичних слайдерів.

---

## Результати тестування

Всі юніт-тести пройшли успішно:

```
=== RUN   TestIsPathTarget
--- PASS: TestIsPathTarget (0.00s)
=== RUN   TestMatchPathTarget
--- PASS: TestMatchPathTarget (0.00s)
=== RUN   TestFindSliderForSession
--- PASS: TestFindSliderForSession (0.00s)
=== RUN   TestApplyStoredSliderVolume
--- PASS: TestApplyStoredSliderVolume (0.00s)
=== RUN   TestStreamPCMode_MaximizeMappedAppVolumesOnly
--- PASS: TestStreamPCMode_MaximizeMappedAppVolumesOnly (0.00s)
=== RUN   TestStreamPCMode_SpawnNewProcess
--- PASS: TestStreamPCMode_SpawnNewProcess (0.00s)
=== RUN   TestStreamPCMode_MasterSliderMove
--- PASS: TestStreamPCMode_MasterSliderMove (0.00s)
=== RUN   TestStreamPCMode_ToggleBackToNormal
--- PASS: TestStreamPCMode_ToggleBackToNormal (0.00s)
=== RUN   TestConfig_MasterMappingAndPreferences
--- PASS: TestConfig_MasterMappingAndPreferences (0.02s)
PASS
ok  	github.com/omriharel/deej/pkg/deej	1.123s
```

### Збірка бінарних файлів
Успішно зібрано свіжі бінарники:
- `deej-debug-amd64.exe` (консольна версія для розробки з детальними логами)
- `deej-amd64.exe` (релізна версія з підтримкою трею)
