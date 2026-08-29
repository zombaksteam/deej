# Implementation Plan: Stream PC Mode & Master Mapping

Додавання режиму **Stream PC Mode** до Deej для роботи у dual-PC конфігураціях: перемикач у системному треї (з чекбоксом), збереження стану в `preferences.yaml`, нова опція `master_mapping` у `config.yaml`, ексклюзивний контроль Master-гучності одним повзунком та автоматичне встановлення 100% гучності **виключно для процесів, зазначених у `slider_mapping` або які підпадають під його шаблони**.

## User Review Required

> [!IMPORTANT]
> - **Гучність 100% тільки для процесів із `slider_mapping`**:
>   - При активації режиму `Stream PC Mode` (або при появі нового процесу) гучність 100% виставляється **виключно тим процесам, які зазначені в `slider_mapping` або підпадають під його шляхи/маски** (наприклад, `discord.exe`, `chrome.exe`, `C:\Program Files (x86)\Steam\steamapps\common\*`).
>   - Будь-які сторонні процеси, не описані в конфігу, **не змінюються**.
> - **Контроль Master-гучності**:
>   - У режимі `Stream PC Mode` контролюється **лише Master** за допомогою повзунка з індексом із `master_mapping` (наприклад, `3`).
>   - Усі інші повзунки ігноруються, а додатки залишаються на 100%.
> - **Збереження стану**:
>   - Стан `stream_pc_mode: true/false` зберігається у `preferences.yaml` поруч із програмою.
> - **Відновлення при вимкненні**:
>   - При вимкненні прапорця повертається звичайний режим, і для всіх процесів знову застосовуються збережені значення їхніх слайдерів.

---

## Proposed Changes

### 1. Конфігурація та збереження налаштувань

#### [MODIFY] [config.go](file:///c:/Users/Zombak/Documents/GitHub/deej/pkg/deej/config.go)
- Додати поля `MasterMapping int` та `StreamPCMode bool` до `CanonicalConfig`.
- Додати константи ключів `master_mapping` та `stream_pc_mode`.
- Зберігати `preferences.yaml` у кореневій папці (`.`).
- Реалізувати метод `SavePreferences() error` для запису `stream_pc_mode` у `preferences.yaml`.
- Зчитувати `master_mapping` із `config.yaml` та `stream_pc_mode` із `preferences.yaml`.

#### [MODIFY] [config.yaml](file:///c:/Users/Zombak/Documents/GitHub/deej/config.yaml)
- Додати опцію `master_mapping: 3` відразу після `slider_mapping:` з коментарями.

---

### 2. Ядро Deej та інтерфейс сесій

#### [MODIFY] [session.go](file:///c:/Users/Zombak/Documents/GitHub/deej/pkg/deej/session.go)
- Додати методи `Master() bool` та `System() bool` до інтерфейсу `Session` та структури `baseSession`.

#### [MODIFY] [deej.go](file:///c:/Users/Zombak/Documents/GitHub/deej/pkg/deej/deej.go)
- Додати метод `SetStreamPCMode(enabled bool) error`:
  - Зберігає стан у `preferences.yaml`.
  - Викликає `d.sessions.onStreamPCModeChanged(enabled)`.
  - Надсилає Toast-повідомлення в Windows.

---

### 3. Меню системного трею

#### [MODIFY] [tray.go](file:///c:/Users/Zombak/Documents/GitHub/deej/pkg/deej/tray.go)
- Додати пункт меню `Stream PC Mode` із чекбоксом.
- Синхронізувати стан чекбоксу з `preferences.yaml` при старті та при кліках користувача.

---

### 4. Логіка мапінгу та автоматичного виставлення 100% гучності

#### [MODIFY] [session_map.go](file:///c:/Users/Zombak/Documents/GitHub/deej/pkg/deej/session_map.go)
- Реалізувати функцію `sessionMatchesMappingTarget(session Session) bool`:
  - Перевіряє точний збіг назви exe, збіг за маскою шляху директорій (`C:\Steam\*`), або спеціальні таргети, якщо вони зазначені у `slider_mapping`.
  - Якщо процес не зазначений у конфігу — повертає `false`.
- Оновити `onStreamPCModeChanged(enabled bool)`:
  - При `enabled == true`:
    - Встановлює Master-гучність зі слайдера `master_mapping`.
    - Для всіх процесів, де `sessionMatchesMappingTarget(session) == true` і гучність < 1.0 — встановлює `1.0` (100%).
  - При `enabled == false`:
    - Відновлює збережені позиції слайдерів для всіх змаплених процесів.
- Оновити `applyStoredSliderVolume(session Session)`:
  - У режимі `StreamPCMode`:
    - Якщо `session.Master()` — застосовує гучність слайдера `master_mapping`.
    - Якщо `sessionMatchesMappingTarget(session)` — встановлює `1.0` (100%), якщо поточна гучність менша за 1.0.
    - Якщо сесія не підпадає під мапінг — не чіпає її.
- Оновити `handleSliderMoveEvent(event SliderMoveEvent)`:
  - У режимі `StreamPCMode`: керує виключно Master-сесією при русі повзунка з `master_mapping`, решту повзунків ігнорує.

---

### 5. Тестування

#### [MODIFY] [session_map_test.go](file:///c:/Users/Zombak/Documents/GitHub/deej/pkg/deej/session_map_test.go)
- Додати тести:
  - `TestStreamPCMode_MaximizeMappedAppVolumesOnly`: перевірка, що 100% отримують **лише** ті процеси, які є в `slider_mapping` або підпадають під його маски шляхів, а незмаплені сторонні процеси залишаються незмінними.
  - `TestStreamPCMode_SpawnNewProcess`: перевірка 100% для нових процесів із шаблону.
  - `TestStreamPCMode_MasterSlider`: перевірка керування Master через `master_mapping`.
  - `TestStreamPCMode_ToggleBackToNormal`: перевірка повернення гучностей при вимкненні режиму.

---

## Verification Plan

### Automated Tests
1. Запуск повного пакету юніт-тестів:
   ```powershell
   go test -v ./pkg/deej
   ```
2. Збірка виконуваних бінарників:
   ```powershell
   go build -o deej-debug-amd64.exe ./pkg/deej/cmd
   go build -ldflags="-H windowsgui" -o deej-amd64.exe ./pkg/deej/cmd
   ```
