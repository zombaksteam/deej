#include <SoftwareSerial.h>

const int NUM_SLIDERS = 6;
const int analogInputs[NUM_SLIDERS] = {A0, A1, A2, A3, A4, A5};
SoftwareSerial mySerial(2, 3); // RX, TX

int lastRawValues[NUM_SLIDERS];       // Зберігаємо останні стабільні сирі значення
int lastSentPercentages[NUM_SLIDERS]; // Зберігаємо останні відправлені відсотки
const int threshold = 7;              // Поріг для сирих даних (0-1023)

// Налаштування "мертвих зон" на кінцях слайдера
const int sensorMin = 10;   // Все що нижче 10 -> стане 0%
const int sensorMax = 1013; // Все що вище 1013 -> стане 100%

const unsigned long HEARTBEAT_INTERVAL = 2000; // Надіслати стан раз на 2 сек для підтримки зв'язку (heartbeat)
unsigned long lastHeartbeatTime = 0;

void setup() {
  for (int i = 0; i < NUM_SLIDERS; i++) {
    pinMode(analogInputs[i], INPUT);
    lastRawValues[i] = -100;      // Щоб при старті перше значення завжди зчиталось
    lastSentPercentages[i] = -1;  // Щоб при старті поточні відсотки гарантовано відправились
  }
  Serial.begin(9600);
  mySerial.begin(9600);
}

void loop() {
  bool needsUpdate = false;

  for (int i = 0; i < NUM_SLIDERS; i++) {
    int currentRaw = analogRead(analogInputs[i]);

    if (abs(currentRaw - lastRawValues[i]) > threshold) {
      lastRawValues[i] = currentRaw;

      // 1. Інвертуємо значення (як у тебе було)
      int invertedRaw = 1023 - currentRaw;

      // 2. Використовуємо зміщений діапазон для "прилипання"
      // map(значення, мін_вхід, макс_вхід, мін_вихід, макс_вихід)
      int mappedValue = map(invertedRaw, sensorMin, sensorMax, 0, 100);

      // 3. Обмежуємо результат, щоб не було -1% або 101%
      int currentPercentage = constrain(mappedValue, 0, 100);

      if (currentPercentage != lastSentPercentages[i]) {
        lastSentPercentages[i] = currentPercentage;
        needsUpdate = true;
      }
    }
  }

  unsigned long now = millis();
  if (needsUpdate || (now - lastHeartbeatTime >= HEARTBEAT_INTERVAL)) {
    sendSliderValues();
    lastHeartbeatTime = now;
  }

  delay(50);
}

void sendSliderValues() {
  String builtString = "";
  for (int i = 0; i < NUM_SLIDERS; i++) {
    builtString += String(lastSentPercentages[i]);
    if (i < NUM_SLIDERS - 1) {
      builtString += "|";
    }
  }
  Serial.println(builtString);
  mySerial.println(builtString);
}
