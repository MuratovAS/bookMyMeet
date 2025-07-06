
document.addEventListener('DOMContentLoaded', function() {
    const calendarBody = document.getElementById('calendarBody');
    const monthSelect = document.getElementById('monthSelect');
    const timezoneSelect = document.getElementById('timezoneSelect');
    const timeSlots = document.getElementById('timeSlots');
    const slotsGrid = document.getElementById('slotsGrid');
    const bookingForm = document.getElementById('bookingForm');
    const cancelForm = document.getElementById('cancelForm');
    const modal = document.getElementById('modal');
    
    let currentTimezone = 1; // Default CET (UTC+1/+2)
    let isDST = true; // Daylight saving time flag
    
    let currentDate = new Date();
    let selectedDate = null;
    let selectedTime = null;
    let availableSlots = {};
    
    // Time conversion considering daylight saving time
    function convertTimeToTimezone(time, timezoneOffset) {
        const [hours, minutes] = time.split(':').map(Number);
        let offset = timezoneOffset;
        
        // For CET we consider daylight saving time (+1 hour)
        if (timezoneOffset === 1 && isDST) {
            offset += 1;
        }
        
        let newHours = hours + offset;
        
        if (newHours >= 24) newHours -= 24;
        if (newHours < 0) newHours += 24;
        
        return `${String(newHours).padStart(2, '0')}:${String(minutes).padStart(2, '0')}`;
    }

    // Initialization
    initializeSelectors();
    loadAvailableSlots();
    
    // Timezone change handler
    timezoneSelect.addEventListener('change', function() {
        currentTimezone = parseInt(this.value);
        // Determine daylight saving time for CET (March-October)
        const now = new Date();
        const month = now.getMonth();
        isDST = (currentTimezone === 1) && (month >= 2 && month <= 9);
        
        if (selectedDate) {
            showTimeSlots(selectedDate);
        }
    });
    
    function initializeSelectors() {
        // Set current month and year
        const currentMonth = currentDate.getMonth();
        const currentYear = currentDate.getFullYear();
        monthSelect.innerHTML = '';
        
        // Add only current and next month
        for (let i = 0; i < 2; i++) {
            const month = (currentMonth + i) % 12;
            const year = month < currentMonth ? currentYear + 1 : currentYear;
            const option = document.createElement('option');
            option.value = `${month},${year}`;
            option.textContent = new Date(0, month).toLocaleString('default', {month: 'long'});
            if (i === 0) option.selected = true;
            monthSelect.appendChild(option);
        }
        
        // Month change handler
        monthSelect.addEventListener('change', function() {
            const [month, year] = this.value.split(',').map(Number);
            generateCalendar(month, year);
        });
        
        generateCalendar(currentMonth, currentYear);
    }
    
    async function loadAvailableSlots() {
        try {
            const response = await fetch('/api/available');
            availableSlots = await response.json();
            const [month, year] = monthSelect.value.split(',').map(Number);
            generateCalendar(month, year);
        } catch (error) {
            console.error('Error loading time slots:', error);
        }
    }
    
    function generateCalendar(month, year) {
        calendarBody.innerHTML = '';
        
        const firstDay = new Date(year, month, 1);
        const lastDay = new Date(year, month + 1, 0);
        const firstDayWeek = (firstDay.getDay() + 6) % 7; // Monday = 0
        
        // Previous month days
        const prevMonth = new Date(year, month, 0);
        for (let i = firstDayWeek - 1; i >= 0; i--) {
            const day = document.createElement('div');
            day.className = 'calendar-day other-month';
            day.textContent = prevMonth.getDate() - i;
            calendarBody.appendChild(day);
        }
        
        // Current month days
        for (let day = 1; day <= lastDay.getDate(); day++) {
            const dayElement = document.createElement('div');
            dayElement.className = 'calendar-day';
            dayElement.textContent = day;
            
            const currentDayDate = new Date(year, month, day);
            const dateStr = formatDateForAPI(currentDayDate);
            const dayOfWeek = currentDayDate.getDay();
            
            // Check if there are available slots
            if (availableSlots[dateStr] && availableSlots[dateStr].length > 0) {
                const slotsCount = availableSlots[dateStr].length;
                if (slotsCount < 3) {
                    dayElement.classList.add('has-slots-few');
                } else if (slotsCount <= 6) {
                    dayElement.classList.add('has-slots-some');
                } else {
                    dayElement.classList.add('has-slots-many');
                }
            } else {
                dayElement.classList.add('disabled');
            }
            
            // Weekend days
            if (dayOfWeek === 0 ) { //|| dayOfWeek === 6
                dayElement.classList.add('disabled');
            }
            
            // Past dates
            if (currentDayDate < new Date().setHours(0,0,0,0)) {
                dayElement.classList.add('disabled');
            }
            
            // Click handler
            if (!dayElement.classList.contains('disabled')) {
                dayElement.addEventListener('click', function() {
                    selectDate(currentDayDate, dayElement);
                });
            }
            
            calendarBody.appendChild(dayElement);
        }
        
        // Next month days
        const totalCells = calendarBody.children.length;
        const remainingCells = 42 - totalCells;
        
        for (let day = 1; day <= remainingCells; day++) {
            const dayElement = document.createElement('div');
            dayElement.className = 'calendar-day other-month';
            dayElement.textContent = day;
            calendarBody.appendChild(dayElement);
        }
    }
    
    function selectDate(date, element) {
        // Remove previous selection
        document.querySelectorAll('.calendar-day.selected').forEach(el => {
            el.classList.remove('selected');
        });
        
        // Select new date
        element.classList.add('selected');
        selectedDate = date;
        selectedTime = null;
        
        // Show available time slots
        showTimeSlots(date);
    }
    
    function showTimeSlots(date) {
        const dateStr = formatDateForAPI(date);
        const slots = availableSlots[dateStr] || [];
        
        slotsGrid.innerHTML = '';
        
        if (slots.length === 0) {
            timeSlots.style.display = 'none';
            return;
        }
        
        timeSlots.style.display = 'block';
        
        slots.forEach(time => {
            const slotElement = document.createElement('div');
            slotElement.className = 'time-slot';
            
            // Convert time to selected timezone
            const convertedTime = convertTimeToTimezone(time, currentTimezone);
            slotElement.textContent = convertedTime;
            
            slotElement.addEventListener('click', function() {
                selectTimeSlot(time, slotElement); // Store original time for API
            });
            
            slotsGrid.appendChild(slotElement);
        });
        
        updateBookingButton();
    }
    
    function selectTimeSlot(time, element) {
        // Remove previous selection
        document.querySelectorAll('.time-slot.selected').forEach(el => {
            el.classList.remove('selected');
        });
        
        // Select new time
        element.classList.add('selected');
        selectedTime = time;
        
        updateBookingButton();
    }
    
    function updateBookingButton() {
        const bookBtn = document.querySelector('.book-btn');
        const formInputs = document.querySelectorAll('.booking-form .form-input');
        
        if (selectedDate && selectedTime) {
            bookBtn.disabled = false;
            formInputs.forEach(input => {
                input.disabled = false;
            });
        } else {
            bookBtn.disabled = true;
            formInputs.forEach(input => {
                input.disabled = true;
            });
        }
    }
    
    function formatDateForAPI(date) {
        return date.getFullYear() + '-' + 
               String(date.getMonth() + 1).padStart(2, '0') + '-' + 
               String(date.getDate()).padStart(2, '0');
    }
    
    // CSRF token handling
    let csrfToken = '';
    
    async function getCSRFToken() {
        try {
            const response = await fetch('/api/csrf-token', {
                credentials: 'include'
            });
            const data = await response.json();
            csrfToken = data.token;
        } catch (error) {
            console.error('Error getting CSRF token:', error);
        }
    }
    
    // Initialize CSRF token
    getCSRFToken();
    
    // Booking form handling
    bookingForm.addEventListener('submit', async function(e) {
        e.preventDefault();
        
        if (!selectedDate || !selectedTime) {
            showModal('Error', 'Please select date and time');
            return;
        }
        
        const formData = new FormData(this);
        const bookingData = {
            date: formatDateForAPI(selectedDate),
            time: selectedTime,
            topic: escapeHtml(formData.get('topic')),
            fullName: escapeHtml(formData.get('fullName')),
            contactInfo: escapeHtml(formData.get('contactInfo')),
            _csrf: csrfToken
        };
        
        try {
            const response = await fetch('/api/booking', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': csrfToken
                },
                credentials: 'include',
                body: JSON.stringify(bookingData)
            });
            
            const result = await response.json();
            
            if (result.success) {
                const convertedTime = convertTimeToTimezone(selectedTime, currentTimezone);
                showModal('Booking successful!', 
                    `You are booked for ${selectedDate.toLocaleDateString('en-US')} at ${convertedTime}`, 
                    result.code);
                
                // Clear form and selection
                this.reset();
                clearSelection();
                loadAvailableSlots(); // Reload available slots
            } else {
                showModal('Error', result.error || 'Booking failed');
            }
        } catch (error) {
            showModal('Error', 'Request failed');
        }
    });
    
    // Cancel form handling
    const cancelCodeInput = document.getElementById('cancelCode');
    const cancelBtn = document.querySelector('.cancel-btn');
    
    // Code length validation and button disabling
    cancelCodeInput.addEventListener('input', function() {
        if (this.value.length != 8){
            cancelBtn.disabled = true
        }
        else {
            cancelBtn.disabled = false
            setLocalStorage('cancelCode', this.value);
        }
        if (this.value.length == 0){
            setLocalStorage('cancelCode', this.value);
        }
    });
    
    // LocalStorage functions
    function setLocalStorage(key, value) {
        try {
            localStorage.setItem(key, value);
        } catch (e) {
            console.error('Error saving to localStorage:', e);
        }
    }

    function getLocalStorage(key) {
        try {
            return localStorage.getItem(key);
        } catch (e) {
            console.error('Error reading from localStorage:', e);
            return null;
        }
    }

    // Check localStorage on load
    const savedCode = getLocalStorage('cancelCode');
    if (savedCode) {
        cancelCodeInput.value = savedCode;
        cancelBtn.disabled = savedCode.length != 8;
    }


    // HTML escaping function using DOM API for maximum reliability
    function escapeHtml(unsafe) {
        if (unsafe == null) return '';
        const div = document.createElement('div');
        div.textContent = unsafe.toString();
        return div.innerHTML;
    }

    cancelForm.addEventListener('submit', async function(e) {
        e.preventDefault();
        
        const formData = new FormData(this);
        const code = escapeHtml(formData.get('code'));
        const cancelData = { 
            code,
            _csrf: csrfToken
        };
        
        try {
            const response = await fetch('/api/cancel', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'X-CSRF-Token': csrfToken
                },
                credentials: 'include',
                body: JSON.stringify(cancelData)
            });
            
            const result = await response.json();
            
            if (result.success) {
                cancelBtn.disabled = true;
                setLocalStorage('cancelCode', "");
                showModal('Booking canceled', 'Your booking has been canceled');
                this.reset();
                loadAvailableSlots(); // Reload available slots
            } else {
                showModal('Error', result.error || 'Cancellation failed');
            }
        } catch (error) {
            showModal('Error', 'Request failed');
        }
    });
    
    function clearSelection() {
        selectedDate = null;
        selectedTime = null;
        document.querySelectorAll('.calendar-day.selected, .time-slot.selected').forEach(el => {
            el.classList.remove('selected');
        });
        timeSlots.style.display = 'none';
        updateBookingButton();
    }
    
    function showModal(title, message, code = null) {
        document.getElementById('modalTitle').textContent = title;
        document.getElementById('modalMessage').textContent = message;
        
        const modalCode = document.getElementById('modalCode');
        if (code) {
            document.getElementById('codeDisplay').textContent = code;
            modalCode.style.display = 'block';
            // Auto-fill cancel field
            cancelCodeInput.value = code;
            cancelBtn.disabled = false;
            setLocalStorage('cancelCode', code);
        } else {
            modalCode.style.display = 'none';
        }
        
        modal.style.display = 'block';
    }
    
    // Modal close handler
    document.getElementById('closeModal').addEventListener('click', function() {
        modal.style.display = 'none';
    });
    
    document.getElementById('modalBtn').addEventListener('click', function() {
        modal.style.display = 'none';
    });
    
    // Close modal on outside click
    modal.addEventListener('click', function(e) {
        if (e.target === modal) {
            modal.style.display = 'none';
        }
    });
});
